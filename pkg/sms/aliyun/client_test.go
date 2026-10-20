package aliyun

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" // #nosec G401,G505 -- 与驱动签名算法一致，仅用于测试校验。
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/sms"
)

func TestPercentEncode(t *testing.T) {
	tests := map[string]string{
		"a b":     "a%20b",
		"a*b":     "a%2Ab",
		"a~b":     "a~b",
		"a/b":     "a%2Fb",
		"a+b":     "a%2Bb",
		"中文":      "%E4%B8%AD%E6%96%87",
		"2006-01": "2006-01",
	}
	for input, want := range tests {
		if got := percentEncode(input); got != want {
			t.Fatalf("percentEncode(%q)=%q want %q", input, got, want)
		}
	}
}

func TestCanonicalQuerySortsKeys(t *testing.T) {
	values := url.Values{}
	values.Set("b", "2")
	values.Set("a", "1")
	values.Set("A", "0")
	if got, want := canonicalQuery(values), "A=0&a=1&b=2"; got != want {
		t.Fatalf("canonicalQuery=%q want %q", got, want)
	}
}

func TestSignIsDeterministic(t *testing.T) {
	values := url.Values{}
	values.Set("Action", "SendSms")
	values.Set("PhoneNumbers", "8613800138000")
	values.Set("TemplateParam", `{"code":"1234"}`)

	got := sign(values, "secret")
	mac := hmac.New(sha1.New, []byte("secret&"))
	mac.Write([]byte(stringToSign(values)))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if got != want {
		t.Fatalf("sign=%q want %q", got, want)
	}
	again := sign(values, "secret")
	if again != got {
		t.Fatal("signature must be deterministic")
	}
	if other := sign(values, "other"); other == got {
		t.Fatal("different secrets must produce different signatures")
	}
}

func TestOpenValidatesConfig(t *testing.T) {
	ctx := context.Background()
	if _, err := Open(ctx, sms.Config{Driver: "aliyun"}); !errors.Is(err, sms.ErrInvalidConfig) {
		t.Fatalf("err=%v want ErrInvalidConfig", err)
	}
	if _, err := Open(ctx, sms.Config{Driver: "aliyun", Aliyun: sms.AliyunConfig{AccessKeyID: "ak"}}); !errors.Is(err, sms.ErrInvalidConfig) {
		t.Fatalf("err=%v want ErrInvalidConfig", err)
	}
	if _, err := Open(ctx, sms.Config{Driver: "aliyun", Aliyun: sms.AliyunConfig{
		AccessKeyID: "ak", AccessKeySecret: "sk",
	}}); !errors.Is(err, sms.ErrInvalidConfig) {
		t.Fatalf("err=%v want ErrInvalidConfig", err)
	}
	if _, err := Open(ctx, sms.Config{Driver: "aliyun", Aliyun: sms.AliyunConfig{
		Endpoint: "ftp://example.com", AccessKeyID: "ak", AccessKeySecret: "sk", SignName: "签名",
	}}); !errors.Is(err, sms.ErrInvalidConfig) {
		t.Fatalf("err=%v want ErrInvalidConfig", err)
	}
}

func TestSendSuccess(t *testing.T) {
	var mu sync.Mutex
	var captured url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		captured = r.URL.Query()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Code":"OK","Message":"OK","RequestId":"req-1","BizId":"biz-1"}`))
	}))
	defer server.Close()

	sender, err := Open(context.Background(), testConfig(server.URL))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = sender.Close() }()

	msg := testMessage()
	receipt, err := sender.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if receipt.Provider != providerName || receipt.MessageID != "biz-1" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if receipt.AcceptedAt.IsZero() {
		t.Fatal("accepted_at is zero")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, key := range []string{"Signature", "SignatureNonce", "Timestamp", "Action", "Version"} {
		if captured.Get(key) == "" {
			t.Fatalf("missing query param %s", key)
		}
	}
	if got := captured.Get("PhoneNumbers"); got != "8613800138000" {
		t.Fatalf("phone=%q", got)
	}
	if got := captured.Get("TemplateParam"); got != `{"code":"1234"}` {
		t.Fatalf("template param=%q", got)
	}
	if got := captured.Get("OutId"); got != msg.IdempotencyKey {
		t.Fatalf("out id=%q", got)
	}
	unsigned := url.Values{}
	for key, values := range captured {
		if key == signatureParam {
			continue
		}
		unsigned[key] = values
	}
	if got, want := captured.Get(signatureParam), sign(unsigned, "sk"); got != want {
		t.Fatalf("signature mismatch: got %q want %q", got, want)
	}
}

func TestSendMapsAPIErrors(t *testing.T) {
	tests := []struct {
		name      string
		code      string
		wantIs    error
		wantCode  pkgerrors.Code
		retryable bool
	}{
		{name: "rate limited", code: "isv.BUSINESS_LIMIT_CONTROL", wantCode: pkgerrors.CodeResourceExhausted, retryable: true},
		{name: "illegal phone", code: "isv.MOBILE_NUMBER_ILLEGAL", wantCode: pkgerrors.CodeInvalidArgument},
		{name: "template rejected", code: "isv.SMS_TEMPLATE_ILLEGAL", wantIs: sms.ErrRejected, wantCode: pkgerrors.CodeFailedPrecondition},
		{name: "no balance", code: "isv.AMOUNT_NOT_ENOUGH", wantCode: pkgerrors.CodePermissionDenied},
		{name: "system error", code: "isv.SYSTEM_ERROR", wantCode: pkgerrors.CodeUnavailable, retryable: true},
		{name: "unknown", code: "isv.UNKNOWN", wantCode: pkgerrors.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"Code":"` + tt.code + `","Message":"boom","RequestId":"req-2"}`))
			}))
			defer server.Close()
			sender, err := Open(context.Background(), testConfig(server.URL))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = sender.Close() }()
			_, err = sender.Send(context.Background(), testMessage())
			if err == nil {
				t.Fatal("expected error")
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Fatalf("err=%v want %v", err, tt.wantIs)
			}
			if !pkgerrors.IsCode(err, tt.wantCode) {
				t.Fatalf("code=%s want %s (err=%v)", pkgerrors.CodeOf(err), tt.wantCode, err)
			}
			if got := pkgerrors.RetryableOf(err); got != tt.retryable {
				t.Fatalf("retryable=%v want %v", got, tt.retryable)
			}
			var providerErr *sms.ProviderError
			if !errors.As(err, &providerErr) || providerErr.Provider != providerName || providerErr.Code != tt.code {
				t.Fatalf("unexpected provider error: %+v", providerErr)
			}
			if strings.Contains(err.Error(), "13800138000") {
				t.Fatalf("error leaks recipient: %v", err)
			}
		})
	}
}

func TestSendHTTPStatusMapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	sender, err := Open(context.Background(), testConfig(server.URL))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = sender.Close() }()
	_, err = sender.Send(context.Background(), testMessage())
	if !pkgerrors.IsCode(err, pkgerrors.CodeResourceExhausted) {
		t.Fatalf("err=%v want ResourceExhausted", err)
	}
	if got := sms.RetryAfterOf(err); got != 3*time.Second {
		t.Fatalf("retry after=%s want 3s", got)
	}
}

func TestSendRejectsRawContent(t *testing.T) {
	sender, err := Open(context.Background(), testConfig("http://127.0.0.1:1"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = sender.Close() }()
	msg := testMessage()
	msg.TemplateID = ""
	msg.Params = nil
	msg.Content = "hello"
	if _, err := sender.Send(context.Background(), msg); !errors.Is(err, sms.ErrRejected) {
		t.Fatalf("err=%v want ErrRejected", err)
	}
}

func TestSendHonorsCanceledContext(t *testing.T) {
	sender, err := Open(context.Background(), testConfig("http://127.0.0.1:1"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = sender.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = sender.Send(ctx, testMessage())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context.Canceled", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	sender, err := Open(context.Background(), testConfig("http://127.0.0.1:1"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := sender.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := sender.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := sender.Send(context.Background(), testMessage()); !errors.Is(err, sms.ErrClosed) {
		t.Fatalf("err=%v want ErrClosed", err)
	}
}

func testConfig(endpoint string) sms.Config {
	return sms.Config{
		Driver: "aliyun",
		Clock:  clock.NewManual(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)),
		Aliyun: sms.AliyunConfig{
			Endpoint:        endpoint,
			Region:          "cn-hangzhou",
			AccessKeyID:     "ak",
			AccessKeySecret: "sk",
			SignName:        "测试签名",
		},
	}
}

func testMessage() sms.Message {
	return sms.Message{
		Phone:          "+8613800138000",
		TemplateID:     "SMS_000001",
		Params:         map[string]string{"code": "1234"},
		IdempotencyKey: "sms:aliyun:1",
	}
}
