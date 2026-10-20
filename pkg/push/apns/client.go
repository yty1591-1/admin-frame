package apns

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/httpclient"
	"github.com/admin-frame/admin-frame/pkg/push"
)

const (
	providerName     = "apns"
	defaultEndpoint  = "https://api.push.apple.com"
	maxPayloadBytes  = 4096
	maxResponseBytes = 16 << 10
	apnsIDHeader     = "apns-id"
)

var (
	uuidRE  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	keyIDRE = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
	topicRE = regexp.MustCompile(`^[A-Za-z0-9.\-]{1,155}$`)

	allowedPushTypes = map[string]struct{}{
		"alert": {}, "background": {}, "voip": {}, "complication": {},
		"fileprovider": {}, "mdm": {}, "location": {}, "liveactivity": {},
	}
)

func init() {
	push.Register(providerName, Open)
}

type sender struct {
	cfg        push.Config
	endpoint   string
	httpClient *httpclient.Client
	tokens     *tokenProvider
	clock      clock.Clock
	closed     atomic.Bool
}

// Open 构造 APNs 驱动；配置非法返回包装 ErrInvalidConfig 的错误。
//
// Topic/KeyID/TeamID/PrivateKey 必填；PrivateKey 支持 PEM 内容或文件路径。
// Endpoint 为空时使用生产地址；测试可指向 httptest 服务。
func Open(_ context.Context, cfg push.Config) (push.Sender, error) {
	cfg = cfg.Normalized()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	apnsCfg := cfg.APNS
	topic := strings.TrimSpace(apnsCfg.Topic)
	if !topicRE.MatchString(topic) {
		return nil, fmt.Errorf("%w: apns topic is invalid", push.ErrInvalidConfig)
	}
	keyID := strings.TrimSpace(apnsCfg.KeyID)
	teamID := strings.TrimSpace(apnsCfg.TeamID)
	if !keyIDRE.MatchString(keyID) || !keyIDRE.MatchString(teamID) {
		return nil, fmt.Errorf("%w: apns key id and team id are invalid", push.ErrInvalidConfig)
	}
	keyData, err := loadPrivateKey(apnsCfg.PrivateKey)
	if err != nil {
		return nil, err
	}
	key, err := parsePrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", push.ErrInvalidConfig, err)
	}
	pushType := strings.ToLower(strings.TrimSpace(apnsCfg.PushType))
	if pushType == "" {
		pushType = "alert"
	}
	if _, ok := allowedPushTypes[pushType]; !ok {
		return nil, fmt.Errorf("%w: apns push type %q is invalid", push.ErrInvalidConfig, apnsCfg.PushType)
	}
	endpoint := strings.TrimRight(strings.TrimSpace(apnsCfg.Endpoint), "/")
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return nil, fmt.Errorf("%w: apns endpoint is invalid", push.ErrInvalidConfig)
	}
	client := cfg.HTTPClient
	if client == nil {
		client, err = httpclient.New(httpclient.Config{Timeout: cfg.Timeout, DisableRetry: true})
		if err != nil {
			return nil, fmt.Errorf("%w: apns http client: %v", push.ErrInvalidConfig, err)
		}
	}
	return &sender{
		cfg:        cfg,
		endpoint:   endpoint,
		httpClient: client,
		tokens:     newTokenProvider(key, keyID, teamID, cfg.Clock),
		clock:      cfg.Clock,
	}, nil
}

// Provider 返回驱动名。
func (s *sender) Provider() string { return providerName }

// Close 标记驱动关闭；幂等，不持有长连接资源。
func (s *sender) Close() error {
	s.closed.Store(true)
	return nil
}

// Send 调用 APNs HTTP/2 接口；成功表示 Apple 已受理。
func (s *sender) Send(ctx context.Context, msg push.Message) (push.Receipt, error) {
	start := s.clock.Now()
	if ctx == nil {
		ctx = context.Background()
	}
	if s.closed.Load() {
		return push.Receipt{}, fmt.Errorf("%w: %s", push.ErrClosed, providerName)
	}
	if err := ctx.Err(); err != nil {
		return push.Receipt{}, s.fail(ctx, msg, start, push.MapContextError(providerName, err))
	}
	if msg.Platform != push.PlatformIOS {
		return push.Receipt{}, s.fail(ctx, msg, start,
			fmt.Errorf("%w: apns supports ios only", push.ErrNotSupported))
	}
	if err := msg.Validate(); err != nil {
		return push.Receipt{}, s.fail(ctx, msg, start, err)
	}
	payload, err := push.MarshalPayload(msg)
	if err != nil {
		return push.Receipt{}, s.fail(ctx, msg, start, push.Rejected("apns: encode payload"))
	}
	if len(payload) > maxPayloadBytes {
		return push.Receipt{}, s.fail(ctx, msg, start,
			fmt.Errorf("%w: %w", push.ErrInvalidMessage, pkgerrors.InvalidArgument("apns: payload too large")))
	}
	providerToken, err := s.tokens.token()
	if err != nil {
		return push.Receipt{}, s.fail(ctx, msg, start,
			push.NewProviderError(providerName, "token", 0, pkgerrors.Internal("apns: provider token")))
	}
	apnsID := s.apnsID(msg)
	endpoint := s.endpoint + "/3/device/" + url.PathEscape(msg.Token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return push.Receipt{}, s.fail(ctx, msg, start,
			push.NewProviderError(providerName, "request", 0, pkgerrors.Internal("apns: build request")))
	}
	req.Header.Set("authorization", "bearer "+providerToken)
	req.Header.Set("apns-topic", s.cfg.APNS.Topic)
	req.Header.Set("apns-push-type", s.pushType())
	req.Header.Set("apns-priority", s.priority(msg))
	req.Header.Set("apns-id", apnsID)
	req.Header.Set("content-type", "application/json")
	if msg.TTL > 0 {
		req.Header.Set("apns-expiration", strconv.FormatInt(s.clock.Now().Add(msg.TTL).Unix(), 10))
	}
	if msg.CollapseKey != "" {
		req.Header.Set("apns-collapse-id", msg.CollapseKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return push.Receipt{}, s.fail(ctx, msg, start, s.mapTransportError(ctx, err))
	}
	defer func() { _ = resp.Body.Close() }() // 只读响应体，关闭错误不影响结果。
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return push.Receipt{}, s.fail(ctx, msg, start, s.mapTransportError(ctx, err))
	}
	if resp.StatusCode != http.StatusOK {
		if isProviderTokenError(decodeReason(body)) {
			s.tokens.invalidate()
		}
		return push.Receipt{}, s.fail(ctx, msg, start, mapApnsError(resp.StatusCode, body, resp))
	}
	messageID := resp.Header.Get(apnsIDHeader)
	if messageID == "" {
		messageID = apnsID
	}
	receipt := push.Receipt{Provider: providerName, MessageID: messageID, AcceptedAt: s.clock.Now()}
	s.emit(ctx, msg, start, nil)
	return receipt, nil
}

func (s *sender) pushType() string {
	value := strings.ToLower(strings.TrimSpace(s.cfg.APNS.PushType))
	if value == "" {
		return "alert"
	}
	return value
}

func (s *sender) priority(msg push.Message) string {
	if msg.IsHighPriority() {
		return "10"
	}
	return "5"
}

func (s *sender) apnsID(msg push.Message) string {
	if uuidRE.MatchString(msg.IdempotencyKey) {
		return msg.IdempotencyKey
	}
	return newUUID()
}

func (s *sender) mapTransportError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return push.MapContextError(providerName, ctxErr)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return push.MapContextError(providerName, context.DeadlineExceeded)
	}
	return push.NewProviderError(providerName, "transport", 0, pkgerrors.Unavailable("apns: transport error"))
}

func (s *sender) fail(ctx context.Context, msg push.Message, start time.Time, err error) error {
	s.emit(ctx, msg, start, err)
	return err
}

func (s *sender) emit(ctx context.Context, msg push.Message, start time.Time, err error) {
	if s.cfg.Observer == nil {
		return
	}
	ev := push.Event{
		Provider:  providerName,
		Platform:  string(msg.Platform),
		Operation: "send",
		Result:    "success",
		Duration:  s.clock.Now().Sub(start),
	}
	if err != nil {
		ev.Result = "failure"
		ev.Code = push.CodeOf(err)
		ev.RetryAfter = push.RetryAfterOf(err)
	}
	s.cfg.Observer.Observe(ctx, ev)
}

func loadPrivateKey(value string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: apns private key is required", push.ErrInvalidConfig)
	}
	if strings.Contains(trimmed, "-----BEGIN") {
		return []byte(trimmed), nil
	}
	data, err := os.ReadFile(trimmed) // #nosec G304 -- 配置显式指定的本地私钥路径。
	if err != nil {
		return nil, fmt.Errorf("%w: read apns private key", push.ErrInvalidConfig)
	}
	return data, nil
}

func newUUID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

type errorPayload struct {
	Reason string `json:"reason"`
}

func decodeReason(body []byte) string {
	var payload errorPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return payload.Reason
}

func isProviderTokenError(reason string) bool {
	switch reason {
	case "ExpiredProviderToken", "InvalidProviderToken":
		return true
	default:
		return false
	}
}

func mapApnsError(status int, body []byte, resp *http.Response) error {
	reason := decodeReason(body)
	code := reason
	if code == "" {
		code = strconv.Itoa(status)
	}
	return push.NewProviderError(providerName, code, retryAfter(resp), apnsSemantic(status, reason))
}

func retryAfter(resp *http.Response) time.Duration {
	value := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func apnsSemantic(status int, reason string) error {
	switch reason {
	case "BadDeviceToken", "DeviceTokenNotForTopic", "Unregistered":
		return fmt.Errorf("%w: %w", push.ErrTokenInvalid, pkgerrors.FailedPrecondition("apns: token rejected"))
	case "ExpiredProviderToken", "InvalidProviderToken", "MissingProviderToken":
		return pkgerrors.PermissionDenied("apns: provider token rejected")
	case "TooManyRequests", "TooManyProviderTokenUpdates":
		return pkgerrors.ResourceExhausted("apns: rate limited")
	case "BadCollapseId", "BadExpirationDate", "BadPriority", "BadTopic",
		"PayloadEmpty", "PayloadTooLarge", "TopicDisallowed", "MissingTopic", "BadPath", "MethodNotAllowed":
		return fmt.Errorf("%w: %w", push.ErrRejected, pkgerrors.InvalidArgument("apns: request rejected"))
	case "ServiceUnavailable", "InternalServerError", "Shutdown":
		return pkgerrors.Unavailable("apns: service unavailable")
	}
	switch {
	case status == http.StatusTooManyRequests:
		return pkgerrors.ResourceExhausted("apns: rate limited")
	case status >= http.StatusInternalServerError:
		return pkgerrors.Unavailable("apns: service unavailable")
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return pkgerrors.PermissionDenied("apns: unauthorized")
	default:
		return fmt.Errorf("%w: %w", push.ErrRejected, pkgerrors.FailedPrecondition("apns: request rejected"))
	}
}
