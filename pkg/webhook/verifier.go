package webhook

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
)

// Verifier 校验入站 Webhook 的签名、时间窗与重放。
//
// 构造后只读，可并发使用；零值不可用。校验不读取配置、不做 I/O，
// 仅通过注入的 NonceStore 与 Observer 产生外部交互。
type Verifier struct {
	cfg       Config
	replayTTL time.Duration
}

// NewVerifier 校验配置并构造 Verifier。
//
// 额外要求 Tolerance ∈ (0,24h]、MaxBodyBytes ∈ (0,8MiB] 且 Nonce 非 nil；
// 任一不满足返回包装 ErrInvalidConfig 的错误，应在启动期终止进程。
func NewVerifier(cfg Config) (*Verifier, error) {
	cfg = cfg.Normalized()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := cfg.validateVerifier(); err != nil {
		return nil, err
	}
	return &Verifier{cfg: cfg, replayTTL: 2 * cfg.Tolerance}, nil
}

// Verify 校验已读取的请求头与 body，成功返回投递快照。
//
// body 必须是未经修改的原始字节；ctx 必须非 nil，用于重放存储的取消与超时。
// 失败返回可 errors.Is 匹配的哨兵，且不消费 nonce（重放记录只在全部
// 校验通过后才写入）。
func (v *Verifier) Verify(ctx context.Context, header http.Header, body []byte) (Delivery, error) {
	if v == nil {
		return Delivery{}, invalidConfig("nil verifier")
	}
	start := time.Now()
	delivery, err := v.verify(ctx, header, body)
	v.observe(ctx, start, err)
	return delivery, err
}

// VerifyRequest 读取并校验 r.Body，成功后把 body 复位以便下游再次读取。
//
// 读取上限为 MaxBodyBytes；失败时 body 已被关闭且不再可读。成功后
// r.Body 被替换为内存 Reader，Handler 可直接 io.ReadAll。
func (v *Verifier) VerifyRequest(ctx context.Context, r *http.Request) (Delivery, error) {
	if v == nil {
		return Delivery{}, invalidConfig("nil verifier")
	}
	if r == nil {
		return Delivery{}, invalidConfig("request must not be nil")
	}
	start := time.Now()
	body, err := readBody(r, v.cfg.MaxBodyBytes)
	if err != nil {
		v.observe(ctx, start, err)
		return Delivery{}, err
	}
	delivery, err := v.verify(ctx, r.Header, body)
	v.observe(ctx, start, err)
	if err != nil {
		return Delivery{}, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return delivery, nil
}

// verify 执行实际校验；调用方负责观测与 body 生命周期。
func (v *Verifier) verify(ctx context.Context, header http.Header, body []byte) (Delivery, error) {
	id, timestamp, signatures, err := v.parseHeaders(header)
	if err != nil {
		return Delivery{}, err
	}
	if int64(len(body)) > v.cfg.MaxBodyBytes {
		return Delivery{}, bodyTooLarge(v.cfg.MaxBodyBytes)
	}
	message := signingMessage(id, timestamp, body)
	if !v.matchesAny(signatures, message) {
		return Delivery{}, signatureMismatch()
	}

	sentAt := time.Unix(timestamp, 0)
	now := v.cfg.Clock.Now()
	if sentAt.Before(now.Add(-v.cfg.Tolerance)) {
		return Delivery{}, timestampOutOfWindow(ErrTimestampExpired, v.cfg.Tolerance)
	}
	if sentAt.After(now.Add(v.cfg.Tolerance)) {
		return Delivery{}, timestampOutOfWindow(ErrTimestampFuture, v.cfg.Tolerance)
	}

	first, claimErr := v.cfg.Nonce.Claim(ctx, id, v.replayTTL)
	if claimErr != nil {
		if ctx.Err() != nil {
			return Delivery{}, nonceContextError(ctx)
		}
		return Delivery{}, nonceUnavailable(claimErr)
	}
	if !first {
		return Delivery{}, replayDetected()
	}
	return Delivery{ID: id, Timestamp: sentAt.UTC(), Body: body}, nil
}

// parseHeaders 按固定顺序解析三个签名头；任一缺失或非法立即返回。
func (v *Verifier) parseHeaders(header http.Header) (id string, timestamp int64, signatures [][]byte, err error) {
	if header == nil {
		return "", 0, nil, headerError(ErrMissingHeader, v.cfg.Headers.ID, "is empty")
	}
	id = header.Get(v.cfg.Headers.ID)
	if id == "" {
		return "", 0, nil, headerError(ErrMissingHeader, v.cfg.Headers.ID, "is empty")
	}
	if err := validateID(v.cfg.Headers.ID, id); err != nil {
		return "", 0, nil, err
	}
	rawTimestamp := header.Get(v.cfg.Headers.Timestamp)
	if rawTimestamp == "" {
		return "", 0, nil, headerError(ErrMissingHeader, v.cfg.Headers.Timestamp, "is empty")
	}
	timestamp, err = parseTimestamp(v.cfg.Headers.Timestamp, rawTimestamp)
	if err != nil {
		return "", 0, nil, err
	}
	signatures, err = parseSignatures(v.cfg.Headers.Signature, header.Get(v.cfg.Headers.Signature), v.cfg.Version)
	if err != nil {
		return "", 0, nil, err
	}
	return id, timestamp, signatures, nil
}

// matchesAny 检查是否存在「签名 × 密钥」组合通过校验。
//
// 多层循环使密钥轮换与多签名并存；任一算法错误都按不匹配处理，
// 不向调用方暴露具体失败原因。
func (v *Verifier) matchesAny(signatures [][]byte, message []byte) bool {
	for _, signature := range signatures {
		for _, key := range v.cfg.Keys {
			if v.cfg.Algorithm.Verify(key, message, signature) == nil {
				return true
			}
		}
	}
	return false
}

func (v *Verifier) observe(ctx context.Context, start time.Time, err error) {
	if v.cfg.Observer == nil {
		return
	}
	event := Event{
		Operation: "verify",
		Version:   v.cfg.Version,
		Result:    "success",
		Duration:  time.Since(start),
	}
	if err != nil {
		event.Result = "failure"
		event.Code = strings.ToLower(string(pkgerrors.CodeOf(err)))
		event.Replay = errors.Is(err, ErrReplay)
	}
	observe(ctx, v.cfg.Observer, event)
}

// readBody 读取并关闭请求体，最多 limit 字节；超限返回 ErrBodyTooLarge。
func readBody(r *http.Request, limit int64) (body []byte, err error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}
	// 请求体只读，关闭失败不影响校验结果，也不应覆盖校验错误。
	defer func() { _ = r.Body.Close() }()
	body, err = io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, bodyUnreadable(err)
	}
	if int64(len(body)) > limit {
		return nil, bodyTooLarge(limit)
	}
	return body, nil
}
