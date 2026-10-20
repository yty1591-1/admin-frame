package fcm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
	providerName     = "fcm"
	defaultEndpoint  = "https://fcm.googleapis.com"
	maxResponseBytes = 32 << 10
)

var projectIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{4,28}[a-z0-9]$`)

func init() {
	push.Register(providerName, Open)
}

type sender struct {
	cfg        push.Config
	endpoint   string
	projectID  string
	httpClient *httpclient.Client
	tokens     *oauthTokenProvider
	clock      clock.Clock
	closed     atomic.Bool
}

// Open 构造 FCM 驱动；配置非法返回包装 ErrInvalidConfig 的错误。
//
// CredentialsJSON 支持 Service Account JSON 内容或文件路径；ProjectID 为空时
// 使用凭据中的 project_id。Endpoint 为空时使用官方地址。
func Open(_ context.Context, cfg push.Config) (push.Sender, error) {
	cfg = cfg.Normalized()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	client := cfg.HTTPClient
	var err error
	if client == nil {
		client, err = httpclient.New(httpclient.Config{Timeout: cfg.Timeout, DisableRetry: true})
		if err != nil {
			return nil, fmt.Errorf("%w: fcm http client: %v", push.ErrInvalidConfig, err)
		}
	}
	account, key, err := loadServiceAccount(cfg.FCM.CredentialsJSON)
	if err != nil {
		return nil, err
	}
	projectID := strings.TrimSpace(cfg.FCM.ProjectID)
	if projectID == "" {
		projectID = account.ProjectID
	}
	if !projectIDRE.MatchString(projectID) {
		return nil, fmt.Errorf("%w: fcm project id is invalid", push.ErrInvalidConfig)
	}
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.FCM.Endpoint), "/")
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	if err := validateHTTPURL(endpoint, "endpoint"); err != nil {
		return nil, err
	}
	if err := validateHTTPURL(account.TokenURI, "token uri"); err != nil {
		return nil, err
	}
	return &sender{
		cfg:        cfg,
		endpoint:   endpoint,
		projectID:  projectID,
		httpClient: client,
		tokens:     newOAuthTokenProvider(account.ClientEmail, key, account.TokenURI, client, cfg.Clock),
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

// Send 调用 FCM HTTP v1 接口；成功表示 Google 已受理。
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
	if msg.Platform != push.PlatformAndroid && msg.Platform != push.PlatformWeb {
		return push.Receipt{}, s.fail(ctx, msg, start,
			fmt.Errorf("%w: fcm supports android and web only", push.ErrNotSupported))
	}
	if err := msg.Validate(); err != nil {
		return push.Receipt{}, s.fail(ctx, msg, start, err)
	}
	accessToken, err := s.tokens.token(ctx)
	if err != nil {
		return push.Receipt{}, s.fail(ctx, msg, start, err)
	}
	payload, err := json.Marshal(map[string]any{"message": s.buildMessage(msg)})
	if err != nil {
		return push.Receipt{}, s.fail(ctx, msg, start,
			push.NewProviderError(providerName, "encode", 0, pkgerrors.Internal("fcm: encode payload")))
	}
	endpoint := s.endpoint + "/v1/projects/" + url.PathEscape(s.projectID) + "/messages:send"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return push.Receipt{}, s.fail(ctx, msg, start,
			push.NewProviderError(providerName, "request", 0, pkgerrors.Internal("fcm: build request")))
	}
	req.Header.Set("authorization", "Bearer "+accessToken)
	req.Header.Set("content-type", "application/json")
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
		if resp.StatusCode == http.StatusUnauthorized {
			s.tokens.invalidate()
		}
		var parsed sendResponse
		_ = json.Unmarshal(body, &parsed)
		return push.Receipt{}, s.fail(ctx, msg, start,
			mapSendError(resp.StatusCode, parsed.Error, retryAfter(resp)))
	}
	var parsed sendResponse
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Name == "" {
		return push.Receipt{}, s.fail(ctx, msg, start,
			push.NewProviderError(providerName, "decode", 0, pkgerrors.Internal("fcm: decode response")))
	}
	receipt := push.Receipt{Provider: providerName, MessageID: parsed.Name, AcceptedAt: s.clock.Now()}
	s.emit(ctx, msg, start, nil)
	return receipt, nil
}

func (s *sender) buildMessage(msg push.Message) map[string]any {
	message := map[string]any{"token": msg.Token}
	notification := make(map[string]string, 2)
	if msg.Title != "" {
		notification["title"] = msg.Title
	}
	if msg.Body != "" {
		notification["body"] = msg.Body
	}
	if len(notification) > 0 {
		message["notification"] = notification
	}
	if len(msg.Data) > 0 {
		message["data"] = msg.Data
	}
	priority := "NORMAL"
	apnsPriority := "5"
	if msg.IsHighPriority() {
		priority = "HIGH"
		apnsPriority = "10"
	}
	android := map[string]any{"priority": priority}
	if msg.TTL > 0 {
		android["ttl"] = strconv.FormatInt(int64(msg.TTL/time.Second), 10) + "s"
	}
	if msg.CollapseKey != "" {
		android["collapse_key"] = msg.CollapseKey
	}
	message["android"] = android

	headers := map[string]string{"apns-priority": apnsPriority}
	if msg.TTL > 0 {
		headers["apns-expiration"] = strconv.FormatInt(s.clock.Now().Add(msg.TTL).Unix(), 10)
	}
	aps := make(map[string]any, 4)
	if msg.Title != "" || msg.Body != "" {
		alert := make(map[string]string, 2)
		if msg.Title != "" {
			alert["title"] = msg.Title
		}
		if msg.Body != "" {
			alert["body"] = msg.Body
		}
		aps["alert"] = alert
	}
	if msg.Badge != nil {
		aps["badge"] = *msg.Badge
	}
	if msg.Sound != "" {
		aps["sound"] = msg.Sound
	}
	message["apns"] = map[string]any{"headers": headers, "payload": map[string]any{"aps": aps}}
	return message
}

func (s *sender) mapTransportError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return push.MapContextError(providerName, ctxErr)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return push.MapContextError(providerName, context.DeadlineExceeded)
	}
	return push.NewProviderError(providerName, "transport", 0, pkgerrors.Unavailable("fcm: transport error"))
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

func validateHTTPURL(value, name string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return fmt.Errorf("%w: fcm %s is invalid", push.ErrInvalidConfig, name)
	}
	return nil
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

type sendResponse struct {
	Name  string    `json:"name"`
	Error *fcmError `json:"error"`
}

type fcmError struct {
	Code    int              `json:"code"`
	Status  string           `json:"status"`
	Message string           `json:"message"`
	Details []fcmErrorDetail `json:"details"`
}

type fcmErrorDetail struct {
	ErrorCode string `json:"errorCode"`
}

func mapSendError(status int, payload *fcmError, retryAfter time.Duration) error {
	statusText := ""
	errorCode := ""
	if payload != nil {
		statusText = payload.Status
		for _, detail := range payload.Details {
			if detail.ErrorCode != "" {
				errorCode = detail.ErrorCode
				break
			}
		}
	}
	code := errorCode
	if code == "" {
		code = statusText
	}
	if code == "" {
		code = strconv.Itoa(status)
	}
	return push.NewProviderError(providerName, code, retryAfter, fcmSemantic(status, statusText, errorCode))
}

func fcmSemantic(status int, statusText, errorCode string) error {
	switch {
	case errorCode == "UNREGISTERED" || statusText == "NOT_FOUND":
		return fmt.Errorf("%w: %w", push.ErrTokenInvalid, pkgerrors.FailedPrecondition("fcm: token unregistered"))
	case statusText == "SENDER_ID_MISMATCH":
		return pkgerrors.PermissionDenied("fcm: sender id mismatch")
	case errorCode == "INVALID_ARGUMENT" || statusText == "INVALID_ARGUMENT" || status == http.StatusBadRequest:
		return fmt.Errorf("%w: %w", push.ErrRejected, pkgerrors.InvalidArgument("fcm: invalid argument"))
	case errorCode == "QUOTA_EXCEEDED" || statusText == "RESOURCE_EXHAUSTED" || status == http.StatusTooManyRequests:
		return pkgerrors.ResourceExhausted("fcm: quota exceeded")
	case statusText == "UNAVAILABLE" || statusText == "INTERNAL" || status >= http.StatusInternalServerError:
		return pkgerrors.Unavailable("fcm: service unavailable")
	case statusText == "PERMISSION_DENIED" || statusText == "UNAUTHENTICATED" ||
		status == http.StatusUnauthorized || status == http.StatusForbidden:
		return pkgerrors.PermissionDenied("fcm: unauthorized")
	default:
		return fmt.Errorf("%w: %w", push.ErrRejected, pkgerrors.FailedPrecondition("fcm: request rejected"))
	}
}
