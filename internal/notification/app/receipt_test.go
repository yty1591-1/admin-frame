package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/notification/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// appErrType 别名：断言 apperr 语义码。
type appErrType = apperr.AppError

// receiptFixture 组装「编排已完成受理」的回执测试前置：请求 sent + 渠道明细 accepted。
func receiptFixture(t *testing.T) (*ReceiptService, domain.RequestRepository, domain.DeliveryRepository, *captureObserver) {
	t.Helper()
	requests := persistence.NewMemoryRequestRepository()
	deliveries := persistence.NewMemoryDeliveryRepository()
	observer := &captureObserver{}
	svc, err := NewReceiptService(requests, deliveries, Options{Service: "c-api", Observer: observer})
	if err != nil {
		t.Fatalf("NewReceiptService: %v", err)
	}
	return svc, requests, deliveries, observer
}

// seedAccepted 建一条 sent 通知单 + accepted 渠道明细（带 message_id）。
func seedAccepted(t *testing.T, requests domain.RequestRepository, deliveries domain.DeliveryRepository,
	requestID, messageID string) {
	t.Helper()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	n, err := domain.NewNotification(requestID, "t-1", "tenant.invitation", "user-1",
		"notify:test:"+requestID, []domain.Channel{domain.ChannelSms}, "tenant.invitation",
		"zh-CN", domain.PriorityNormal, now.Add(time.Hour), now)
	if err != nil {
		t.Fatalf("NewNotification: %v", err)
	}
	n, _ = n.Transition(domain.RequestDispatched, "", now)
	n, _ = n.Transition(domain.RequestSent, "", now)
	if err := requests.Create(context.Background(), n); err != nil {
		t.Fatalf("Create: %v", err)
	}
	d, err := domain.NewDelivery("d-"+requestID, requestID, "t-1", domain.ChannelSms, now)
	if err != nil {
		t.Fatalf("NewDelivery: %v", err)
	}
	d, _ = d.Transition(domain.DeliveryAccepted, now)
	d.Provider = "aliyun"
	d.MessageID = messageID
	if err := deliveries.Upsert(context.Background(), d); err != nil {
		t.Fatalf("seed accepted delivery: %v", err)
	}
}

func TestReceiptDeliveredConvergesRequest(t *testing.T) {
	svc, requests, deliveries, _ := receiptFixture(t)
	seedAccepted(t, requests, deliveries, "req-1", "msg-1")
	ctx := context.Background()

	if err := svc.Apply(ctx, ReceiptInput{
		Channel: domain.ChannelSms, MessageID: "msg-1", Delivered: true, ProviderCode: "DELIVRD",
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	n, _ := requests.Get(ctx, "req-1")
	if n.Status != domain.RequestDelivered {
		t.Fatalf("request must converge to delivered, got %s", n.Status)
	}
	d, _ := deliveries.Get(ctx, "req-1", domain.ChannelSms)
	if d.Status != domain.DeliveryDelivered || d.FinishedAt == nil {
		t.Fatalf("delivery must be delivered with finished_at: %+v", d)
	}

	// 重复回执：幂等吸收，不报错也不再变化。
	if err := svc.Apply(ctx, ReceiptInput{
		Channel: domain.ChannelSms, MessageID: "msg-1", Delivered: true,
	}); err != nil {
		t.Fatalf("duplicate receipt must be absorbed, got %v", err)
	}
	d2, _ := deliveries.Get(ctx, "req-1", domain.ChannelSms)
	if !d2.FinishedAt.Equal(*d.FinishedAt) {
		t.Fatal("duplicate receipt must not mutate the delivery")
	}
}

func TestReceiptUndelivered(t *testing.T) {
	svc, requests, deliveries, _ := receiptFixture(t)
	seedAccepted(t, requests, deliveries, "req-1", "msg-1")
	ctx := context.Background()

	if err := svc.Apply(ctx, ReceiptInput{
		Channel: domain.ChannelSms, MessageID: "msg-1", Delivered: false,
		ProviderCode: "EXPIRED", Summary: "handset offline beyond validity",
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	n, _ := requests.Get(ctx, "req-1")
	if n.Status != domain.RequestUndelivered {
		t.Fatalf("request must converge to undelivered, got %s", n.Status)
	}
	d, _ := deliveries.Get(ctx, "req-1", domain.ChannelSms)
	if d.Status != domain.DeliveryUndelivered || d.LastError == "" {
		t.Fatalf("delivery must record failure summary: %+v", d)
	}
}

func TestReceiptUnknownMessageAndValidation(t *testing.T) {
	svc, _, _, _ := receiptFixture(t)
	// 未知消息 ID：404 语义（transport 对供应商转「已受理」）。
	err := svc.Apply(context.Background(), ReceiptInput{
		Channel: domain.ChannelSms, MessageID: "unknown", Delivered: true,
	})
	var appErr *appErrType
	if !errors.As(err, &appErr) || appErr.NumericCode() != 40400 {
		t.Fatalf("unknown message must 404, got %v", err)
	}
	// 空消息 ID / 非法渠道必须 400。
	if err := svc.Apply(context.Background(), ReceiptInput{Channel: domain.ChannelSms}); err == nil {
		t.Fatal("empty message id must be rejected")
	}
	if err := svc.Apply(context.Background(), ReceiptInput{Channel: "fax", MessageID: "m"}); err == nil {
		t.Fatal("unsupported channel must be rejected")
	}
}

func TestReceiptDoesNotDowngradeDelivered(t *testing.T) {
	svc, requests, deliveries, _ := receiptFixture(t)
	seedAccepted(t, requests, deliveries, "req-1", "msg-1")
	ctx := context.Background()
	// 先送达确认，随后到达乱序失败回执：通知单保持 delivered。
	_ = svc.Apply(ctx, ReceiptInput{Channel: domain.ChannelSms, MessageID: "msg-1", Delivered: true})
	_ = svc.Apply(ctx, ReceiptInput{Channel: domain.ChannelSms, MessageID: "msg-1", Delivered: false})
	n, _ := requests.Get(ctx, "req-1")
	if n.Status != domain.RequestDelivered {
		t.Fatalf("delivered must not downgrade, got %s", n.Status)
	}
}
