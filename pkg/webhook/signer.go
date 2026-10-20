package webhook

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Signer 为出站 Webhook 生成签名头。
//
// 构造后只读，可并发使用；零值不可用。签名结果只依赖配置中的 Keys、Version、
// Headers 与 Clock，不做任何 I/O。
type Signer struct {
	cfg Config
}

// NewSigner 校验配置并构造 Signer；不需要 Nonce 与 Tolerance。
//
// 配置非法时返回包装 ErrInvalidConfig 的错误，应在启动期终止进程。
func NewSigner(cfg Config) (*Signer, error) {
	cfg = cfg.Normalized()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Signer{cfg: cfg}, nil
}

// Sign 使用配置时钟的当前时间生成签名头。
func (s *Signer) Sign(id string, body []byte) (http.Header, error) {
	if s == nil {
		return nil, invalidConfig("nil signer")
	}
	return s.SignAt(id, body, s.cfg.Clock.Now())
}

// SignAt 使用指定时间生成签名头，便于测试与重放历史事件。
//
// 返回的 Header 包含 ID、Timestamp 与 Signature；多个密钥会生成多个以空格
// 分隔的签名，接收方任一密钥匹配即通过，从而支持平滑轮换。
// at 为零值或早于 Unix 纪元时返回 ErrInvalidConfig；body 超过 MaxBodyBytes
// 时返回 ErrBodyTooLarge。
func (s *Signer) SignAt(id string, body []byte, at time.Time) (header http.Header, err error) {
	if s == nil {
		return nil, invalidConfig("nil signer")
	}
	if id == "" {
		return nil, headerError(ErrMissingHeader, s.cfg.Headers.ID, "is empty")
	}
	if err := validateID(s.cfg.Headers.ID, id); err != nil {
		return nil, err
	}
	if int64(len(body)) > s.cfg.MaxBodyBytes {
		return nil, bodyTooLarge(s.cfg.MaxBodyBytes)
	}
	if at.IsZero() {
		return nil, invalidConfig("signing time must not be zero")
	}
	seconds := at.Unix()
	if seconds < 0 {
		return nil, invalidConfig("signing time must not precede the Unix epoch")
	}

	message := signingMessage(id, seconds, body)
	signatures := make([]string, 0, len(s.cfg.Keys))
	for _, key := range s.cfg.Keys {
		signature, signErr := s.cfg.Algorithm.Sign(key, message)
		if signErr != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidConfig, signErr)
		}
		signatures = append(signatures, formatSignature(s.cfg.Version, signature))
	}

	header = make(http.Header, 3)
	header.Set(s.cfg.Headers.ID, id)
	header.Set(s.cfg.Headers.Timestamp, strconv.FormatInt(seconds, 10))
	header.Set(s.cfg.Headers.Signature, strings.Join(signatures, " "))
	return header, nil
}

// SignRequest 为 req 写入签名头；同名头会被覆盖。
//
// req 必须非 nil。body 应与随后实际发送的请求体逐字节一致；本方法不读取
// req.Body，调用方需自行保证二者一致。
func (s *Signer) SignRequest(req *http.Request, id string, body []byte) error {
	if req == nil {
		return invalidConfig("request must not be nil")
	}
	header, err := s.Sign(id, body)
	if err != nil {
		return err
	}
	if req.Header == nil {
		req.Header = make(http.Header, len(header))
	}
	for name, values := range header {
		req.Header[name] = append([]string(nil), values...)
	}
	return nil
}
