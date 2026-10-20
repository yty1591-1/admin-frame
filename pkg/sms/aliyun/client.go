package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/httpclient"
	"github.com/admin-frame/admin-frame/pkg/sms"
)

const providerName = "aliyun"

func init() {
	sms.Register(providerName, Open)
}

type sender struct {
	cfg        sms.Config
	endpoint   string
	region     string
	httpClient *httpclient.Client
	clock      clock.Clock
	closed     atomic.Bool
}

// Open 构造阿里云短信驱动；配置非法返回包装 ErrInvalidConfig 的错误。
//
// AccessKeyID/AccessKeySecret/SignName 必填；Endpoint 为空时使用官方地址，
// Region 为空时使用 cn-hangzhou。cfg.HTTPClient 为 nil 时按 cfg.Timeout 新建，
// 并强制关闭自动重试（重试由编排层统一负责）。
func Open(_ context.Context, cfg sms.Config) (sms.Sender, error) {
	cfg = cfg.Normalized()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	aliyunCfg := cfg.Aliyun
	endpoint := strings.TrimRight(strings.TrimSpace(aliyunCfg.Endpoint), "/")
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return nil, fmt.Errorf("%w: aliyun endpoint %q", sms.ErrInvalidConfig, aliyunCfg.Endpoint)
	}
	if strings.TrimSpace(aliyunCfg.AccessKeyID) == "" || strings.TrimSpace(aliyunCfg.AccessKeySecret) == "" {
		return nil, fmt.Errorf("%w: aliyun access key is required", sms.ErrInvalidConfig)
	}
	if strings.TrimSpace(aliyunCfg.SignName) == "" {
		return nil, fmt.Errorf("%w: aliyun sign name is required", sms.ErrInvalidConfig)
	}
	region := strings.TrimSpace(aliyunCfg.Region)
	if region == "" {
		region = defaultRegion
	}
	client := cfg.HTTPClient
	if client == nil {
		client, err = httpclient.New(httpclient.Config{
			Timeout:      cfg.Timeout,
			DisableRetry: true,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: aliyun http client: %v", sms.ErrInvalidConfig, err)
		}
	}
	return &sender{
		cfg:        cfg,
		endpoint:   endpoint,
		region:     region,
		httpClient: client,
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

// Send 调用阿里云 SendSms；成功表示厂商已受理，不代表已送达。
func (s *sender) Send(ctx context.Context, msg sms.Message) (sms.Receipt, error) {
	start := s.clock.Now()
	if ctx == nil {
		ctx = context.Background()
	}
	if s.closed.Load() {
		return sms.Receipt{}, fmt.Errorf("%w: %s", sms.ErrClosed, providerName)
	}
	if err := ctx.Err(); err != nil {
		return sms.Receipt{}, s.fail(ctx, msg, start, sms.MapContextError(providerName, err))
	}
	if err := msg.Validate(); err != nil {
		return sms.Receipt{}, s.fail(ctx, msg, start, err)
	}
	signName := strings.TrimSpace(msg.SignName)
	if signName == "" {
		signName = strings.TrimSpace(s.cfg.Aliyun.SignName)
	}
	if signName == "" {
		return sms.Receipt{}, s.fail(ctx, msg, start, sms.Rejected("sign name is required"))
	}
	if msg.TemplateID == "" {
		return sms.Receipt{}, s.fail(ctx, msg, start, sms.Rejected("aliyun requires a template id"))
	}
	params, err := s.buildParams(msg, signName)
	if err != nil {
		return sms.Receipt{}, s.fail(ctx, msg, start, err)
	}
	nonce, err := newNonce()
	if err != nil {
		return sms.Receipt{}, s.fail(ctx, msg, start,
			sms.NewProviderError(providerName, "nonce", 0, pkgerrors.Internal("aliyun: generate nonce")))
	}
	params.Set("SignatureNonce", nonce)
	params.Set("Timestamp", newTimestamp(s.clock.Now()))
	params.Set(signatureParam, sign(params, s.cfg.Aliyun.AccessKeySecret))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint+"/?"+params.Encode(), http.NoBody)
	if err != nil {
		return sms.Receipt{}, s.fail(ctx, msg, start,
			sms.NewProviderError(providerName, "request", 0, pkgerrors.Internal("aliyun: build request")))
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return sms.Receipt{}, s.fail(ctx, msg, start, s.mapTransportError(err))
	}
	defer func() { _ = resp.Body.Close() }() // 只读响应体，关闭错误不影响结果。
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return sms.Receipt{}, s.fail(ctx, msg, start, s.mapTransportError(err))
	}
	if err := checkHTTPStatus(resp); err != nil {
		return sms.Receipt{}, s.fail(ctx, msg, start, err)
	}
	var payload sendResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return sms.Receipt{}, s.fail(ctx, msg, start,
			sms.NewProviderError(providerName, "decode", 0, pkgerrors.Internal("aliyun: decode response")))
	}
	if payload.Code != "OK" {
		return sms.Receipt{}, s.fail(ctx, msg, start, mapAPIError(payload.Code))
	}
	receipt := sms.Receipt{
		Provider:   providerName,
		MessageID:  firstNonEmpty(payload.BizID, payload.RequestID),
		AcceptedAt: s.clock.Now(),
	}
	s.emit(ctx, msg, start, nil)
	return receipt, nil
}

func (s *sender) buildParams(msg sms.Message, signName string) (url.Values, error) {
	params := url.Values{}
	params.Set("AccessKeyId", s.cfg.Aliyun.AccessKeyID)
	params.Set("Action", apiAction)
	params.Set("Format", apiFormat)
	params.Set("RegionId", s.region)
	params.Set("SignatureMethod", signatureMethod)
	params.Set("SignatureVersion", signatureVersion)
	params.Set("Version", apiVersion)
	params.Set("PhoneNumbers", strings.TrimPrefix(msg.Phone, "+"))
	params.Set("SignName", signName)
	params.Set("TemplateCode", msg.TemplateID)
	if len(msg.Params) > 0 {
		encoded, err := json.Marshal(msg.Params)
		if err != nil {
			return nil, sms.NewProviderError(providerName, "params", 0,
				pkgerrors.Internal("aliyun: encode template params"))
		}
		params.Set("TemplateParam", string(encoded))
	}
	if msg.IdempotencyKey != "" {
		params.Set("OutId", sms.TruncateRunes(msg.IdempotencyKey, outIDMaxRunes))
	}
	return params, nil
}

func checkHTTPStatus(resp *http.Response) error {
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return sms.NewProviderError(providerName, strconv.Itoa(resp.StatusCode), retryAfterHeader(resp),
			pkgerrors.ResourceExhausted("aliyun: too many requests"))
	case resp.StatusCode >= http.StatusInternalServerError:
		return sms.NewProviderError(providerName, strconv.Itoa(resp.StatusCode), 0,
			pkgerrors.Unavailable("aliyun: server error"))
	case resp.StatusCode >= http.StatusBadRequest:
		return sms.NewProviderError(providerName, "http_"+strconv.Itoa(resp.StatusCode), 0,
			pkgerrors.FailedPrecondition("aliyun: request rejected"))
	default:
		return nil
	}
}

func retryAfterHeader(resp *http.Response) time.Duration {
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

func (s *sender) mapTransportError(err error) error {
	if ctxErr := contextErrorOf(err); ctxErr != nil {
		return sms.MapContextError(providerName, ctxErr)
	}
	return sms.NewProviderError(providerName, "transport", 0, pkgerrors.Unavailable("aliyun: transport error"))
}

func contextErrorOf(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return context.DeadlineExceeded
	}
	return nil
}

func (s *sender) fail(ctx context.Context, msg sms.Message, start time.Time, err error) error {
	s.emit(ctx, msg, start, err)
	return err
}

func (s *sender) emit(ctx context.Context, msg sms.Message, start time.Time, err error) {
	if s.cfg.Observer == nil {
		return
	}
	ev := sms.Event{
		Provider:  providerName,
		Operation: "send",
		Template:  msg.TemplateID,
		Result:    "success",
		Duration:  s.clock.Now().Sub(start),
	}
	if err != nil {
		ev.Result = "failure"
		ev.Code = sms.CodeOf(err)
		ev.RetryAfter = sms.RetryAfterOf(err)
	}
	s.cfg.Observer.Observe(ctx, ev)
}

func mapAPIError(code string) error {
	return sms.NewProviderError(providerName, code, 0, aliyunSemantic(code))
}

func aliyunSemantic(code string) error {
	switch code {
	case "isv.BUSINESS_LIMIT_CONTROL", "isv.DAY_LIMIT_CONTROL", "isv.MONTH_LIMIT_CONTROL":
		return pkgerrors.ResourceExhausted("aliyun: rate limited")
	case "isv.MOBILE_NUMBER_ILLEGAL", "isv.MOBILE_COUNT_OVER_LIMIT", "isv.INVALID_PARAMETERS":
		return pkgerrors.InvalidArgument("aliyun: invalid parameter")
	case "isv.SMS_TEMPLATE_ILLEGAL", "isv.SMS_SIGNATURE_ILLEGAL",
		"isv.TEMPLATE_MISSING_PARAMETERS", "isv.TEMPLATE_PARAMS_ILLEGAL":
		return fmt.Errorf("%w: %w", sms.ErrRejected,
			pkgerrors.FailedPrecondition("aliyun: template or signature rejected"))
	case "isv.AMOUNT_NOT_ENOUGH", "isv.ACCOUNT_NOT_EXISTS", "isv.ACCOUNT_ABNORMAL":
		return pkgerrors.PermissionDenied("aliyun: account is not usable")
	case "isv.OUT_OF_SERVICE":
		return pkgerrors.FailedPrecondition("aliyun: service is out of service")
	case "isv.SYSTEM_ERROR", "isp.SYSTEM_ERROR":
		return pkgerrors.Unavailable("aliyun: system error")
	default:
		return pkgerrors.Internal("aliyun: unexpected response code")
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

type sendResponse struct {
	Code      string `json:"Code"`
	Message   string `json:"Message"`
	RequestID string `json:"RequestId"`
	BizID     string `json:"BizId"`
}
