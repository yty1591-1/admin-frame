package callback

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/app"
	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/notification/infrastructure/persistence"
	pkgwebhook "github.com/admin-frame/admin-frame/pkg/webhook"
)

const testSecret = "callback-secret-0123456789abcdef"

func newReceiptFixture(t *testing.T) (*Handler, domain.RequestRepository, domain.DeliveryRepository) {
	t.Helper()
	t.Setenv(EnvCallbackSecret, testSecret)
	requests := persistence.NewMemoryRequestRepository()
	deliveries := persistence.NewMemoryDeliveryRepository()
	receipts, err := app.NewReceiptService(requests, deliveries, app.Options{Service: "c-api"})
	if err != nil {
		t.Fatalf("NewReceiptService: %v", err)
	}
	handler, err := NewHandler(receipts)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return handler, requests, deliveries
}

func seedSent(t *testing.T, requests domain.RequestRepository, deliveries domain.DeliveryRepository) {
	t.Helper()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	n, err := domain.NewNotification("req-1", "t-1", "tenant.invitation", "user-1",
		"notify:test:req-1", []domain.Channel{domain.ChannelSms}, "tenant.invitation",
		"zh-CN", domain.PriorityNormal, now.Add(time.Hour), now)
	if err != nil {
		t.Fatalf("NewNotification: %v", err)
	}
	n, _ = n.Transition(domain.RequestDispatched, "", now)
	n, _ = n.Transition(domain.RequestSent, "", now)
	if err := requests.Create(context.Background(), n); err != nil {
		t.Fatalf("Create: %v", err)
	}
	d, err := domain.NewDelivery("d-1", "req-1", "t-1", domain.ChannelSms, now)
	if err != nil {
		t.Fatalf("NewDelivery: %v", err)
	}
	d, _ = d.Transition(domain.DeliveryAccepted, now)
	d.Provider = "aliyun"
	d.MessageID = "msg-1"
	if err := deliveries.Upsert(context.Background(), d); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
}

// signedRequest 用同盐 Signer 生成合法签名头（eventID 唯一——同 ID 重放
// 会被 nonce 防护拒绝，那是验签层的正确行为）。
func signedRequest(t *testing.T, eventID, body string) *http.Request {
	t.Helper()
	signer, err := pkgwebhook.NewSigner(pkgwebhook.Config{
		Keys:      [][]byte{[]byte(testSecret)},
		Tolerance: defaultTolerance,
	})
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	header, err := signer.Sign(eventID, []byte(body))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/notification-callbacks/receipt", strings.NewReader(body))
	for key, values := range header {
		for _, v := range values {
			req.Header.Add(key, v)
		}
	}
	return req
}

func TestCallbackRequiresSignature(t *testing.T) {
	handler, requests, deliveries := newReceiptFixture(t)
	seedSent(t, requests, deliveries)

	// 无签名请求必须 401（fail closed）。
	req := httptest.NewRequest(http.MethodPost, "/v1/notification-callbacks/receipt",
		strings.NewReader(`{"channel":"sms","message_id":"msg-1","delivered":true}`))
	rec := httptest.NewRecorder()
	handler.Receipt(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned callback must be 401, got %d", rec.Code)
	}
	// 投递不得被改动。
	d, _ := deliveries.Get(context.Background(), "req-1", domain.ChannelSms)
	if d.Status != domain.DeliveryAccepted {
		t.Fatalf("unsigned callback must not mutate delivery, got %s", d.Status)
	}
}

func TestCallbackAppliesSignedReceipt(t *testing.T) {
	handler, requests, deliveries := newReceiptFixture(t)
	seedSent(t, requests, deliveries)

	body := `{"channel":"sms","message_id":"msg-1","delivered":true,"provider_code":"DELIVRD"}`
	rec := httptest.NewRecorder()
	handler.Receipt(rec, signedRequest(t, "evt-1", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("signed receipt status=%d body=%s", rec.Code, rec.Body.String())
	}
	n, _ := requests.Get(context.Background(), "req-1")
	if n.Status != domain.RequestDelivered {
		t.Fatalf("request must converge to delivered, got %s", n.Status)
	}

	// 重复回执（不同 event ID、相同载荷）：200 幂等吸收；
	// 同 event ID 重放会被验签层 nonce 防护拒绝（那是正确行为）。
	rec = httptest.NewRecorder()
	handler.Receipt(rec, signedRequest(t, "evt-2", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("duplicate receipt must be 200, got %d", rec.Code)
	}

	// 未知消息 ID：200 ignored（对供应商语义是已受理，避免重放风暴）。
	unknown := `{"channel":"sms","message_id":"unknown","delivered":true}`
	rec = httptest.NewRecorder()
	handler.Receipt(rec, signedRequest(t, "evt-3", unknown))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ignored") {
		t.Fatalf("unknown message must be 200 ignored, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestCallbackSaltMissingFailsClosed(t *testing.T) {
	// 密钥未配置：构造必须失败（不允许出现无验签的公开写端点）。
	t.Setenv(EnvCallbackSecret, "")
	if _, err := NewHandler(nil); err == nil {
		t.Fatal("nil receipts must be rejected")
	}
	_ = os.Unsetenv(EnvCallbackSecret)
	if _, err := NewHandler(nil); err == nil {
		t.Fatal("missing secret must fail construction")
	}
}
