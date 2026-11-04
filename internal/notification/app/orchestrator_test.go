package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/notification/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/pkg/circuitbreaker"
	"github.com/admin-frame/admin-frame/pkg/mq"
	"github.com/admin-frame/admin-frame/pkg/outbox"
	"github.com/admin-frame/admin-frame/pkg/ratelimit"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

// fakeSender 可编程发送器：按调用次数返回成功或错误。
type fakeSender struct {
	provider string
	mu       sync.Mutex
	calls    int
	failN    int        // 前 failN 次返回 err
	err      *SendError // 失败时返回的错误
}

func (s *fakeSender) Provider() string { return s.provider }

func (s *fakeSender) Send(_ context.Context, _ SendRequest) (SendResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.failN > 0 && s.calls <= s.failN {
		return SendResult{}, s.err
	}
	return SendResult{Provider: s.provider, MessageID: s.provider + "-msg-1"}, nil
}

// captureObserver 记录编排埋点。
type captureObserver struct {
	mu        sync.Mutex
	delivered []string // "channel|result|semantic"
	requests  []string // "scene|status"
	rateHits  int
}

func (o *captureObserver) AuditEmitFailed() {}

func (o *captureObserver) DeliveryObserved(ch domain.Channel, _, result, semantic string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.delivered = append(o.delivered, string(ch)+"|"+result+"|"+semantic)
}

func (o *captureObserver) RateLimited() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rateHits++
}

func (o *captureObserver) RequestObserved(scene string, status domain.RequestStatus) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requests = append(o.requests, scene+"|"+string(status))
}

// recorder 捕获 dispatcher 追加的 Outbox 事件（内存 Store 降级实现）。
type recorder struct {
	mu     sync.Mutex
	events []*outbox.Event
}

func (r *recorder) Append(_ context.Context, e *outbox.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *recorder) ClaimBatch(context.Context, time.Time, time.Duration, int) ([]*outbox.Event, error) {
	return nil, nil
}

func (r *recorder) MarkSucceeded(context.Context, string, time.Time) error { return nil }

func (r *recorder) Reschedule(context.Context, string, int, time.Time, time.Time, string) error {
	return nil
}

func (r *recorder) MarkDead(context.Context, string, time.Time, string) error   { return nil }
func (r *recorder) MarkFailed(context.Context, string, time.Time, string) error { return nil }

func (r *recorder) ReclaimStale(context.Context, time.Time, time.Time) (int, error) { return 0, nil }

func (r *recorder) Stats(context.Context) (outbox.Stats, error) { return outbox.Stats{}, nil }

func testPayload() RequestPayload {
	return RequestPayload{
		SchemaVersion:  SchemaVersionV1,
		RequestID:      "req-1",
		TenantID:       "t1",
		Scene:          "tenant.invitation",
		SceneClass:     "operational",
		RecipientRef:   "user-1",
		Recipient:      Recipient{UserID: "user-1", Email: "bob@example.com"},
		Channels:       []string{"email"},
		TemplateKey:    "tenant.invitation",
		Locale:         "zh-CN",
		Params:         map[string]string{"name": "Acme"},
		IdempotencyKey: "notify:tenant.invitation:inv-1",
		Priority:       "normal",
		Actor:          "admin-1",
		Reference:      "inv-1",
		ExpiresAt:      time.Now().Add(time.Hour),
	}
}

func payloadEvent(t *testing.T, p RequestPayload) *outbox.Event {
	t.Helper()
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &outbox.Event{
		Topic: TopicNotification,
		Envelope: mq.Envelope{
			EventID:        p.RequestID,
			EventType:      TopicNotification,
			TenantID:       p.TenantID,
			IdempotencyKey: p.IdempotencyKey,
			Payload:        body,
		},
	}
}

type testHarness struct {
	orch        *Orchestrator
	requests    domain.RequestRepository
	deliveries  domain.DeliveryRepository
	templates   domain.TemplateRepository
	preferences domain.PreferenceRepository
	blacklist   domain.BlacklistRepository
	observer    *captureObserver
	emitter     *recordEmitter
}

func newHarness(t *testing.T, mutate func(*OrchestratorOptions, map[string]*fakeSender)) *testHarness {
	t.Helper()
	h := &testHarness{
		requests:    persistence.NewMemoryRequestRepository(),
		deliveries:  persistence.NewMemoryDeliveryRepository(),
		templates:   persistence.NewMemoryTemplateRepository(),
		preferences: persistence.NewMemoryPreferenceRepository(),
		blacklist:   persistence.NewMemoryBlacklistRepository(),
		observer:    &captureObserver{},
		emitter:     &recordEmitter{},
	}
	senders := map[string]*fakeSender{
		"smtp": {provider: "smtp"},
		"ses":  {provider: "ses"},
	}
	opts := OrchestratorOptions{
		Options: Options{Service: "worker", Observer: h.observer},
		Route:   map[domain.Channel][]Sender{domain.ChannelEmail: {senders["smtp"]}},
		Emitter: h.emitter,
	}
	if mutate != nil {
		mutate(&opts, senders)
	}
	orch, err := NewOrchestrator(h.requests, h.deliveries, h.templates, h.preferences, h.blacklist, opts)
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	h.orch = orch
	// 注册测试模板。
	if _, err := h.templates.Upsert(context.Background(), mustTemplate(t, "tenant.invitation", domain.ChannelEmail)); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	return h
}

func mustTemplate(t *testing.T, key string, channel domain.Channel) domain.Template {
	t.Helper()
	subject, body := "", "hello {{name}}"
	if channel == domain.ChannelEmail {
		subject = "invite to {{name}}"
	}
	tpl, err := domain.NewTemplate(key, channel, "zh-CN", subject, body, "test",
		time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NewTemplate: %v", err)
	}
	return tpl
}

func TestOrchestratorHappyPath(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.orch.Handle(context.Background(), payloadEvent(t, testPayload())); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	n, err := h.requests.Get(context.Background(), "req-1")
	if err != nil {
		t.Fatalf("Get request: %v", err)
	}
	if n.Status != domain.RequestSent {
		t.Fatalf("request must converge to sent, got %s", n.Status)
	}
	d, err := h.deliveries.Get(context.Background(), "req-1", domain.ChannelEmail)
	if err != nil {
		t.Fatalf("Get delivery: %v", err)
	}
	if d.Status != domain.DeliveryAccepted || d.Provider != "smtp" || d.MessageID == "" {
		t.Fatalf("unexpected delivery: %+v", d)
	}
}

func TestOrchestratorIdempotentRedelivery(t *testing.T) {
	var sender *fakeSender
	h := newHarness(t, func(_ *OrchestratorOptions, s map[string]*fakeSender) { sender = s["smtp"] })
	ctx := context.Background()
	event := payloadEvent(t, testPayload())
	if err := h.orch.Handle(ctx, event); err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	// 重投同一事件：必须幂等吸收，不再发送。
	if err := h.orch.Handle(ctx, event); err != nil {
		t.Fatalf("redelivered Handle: %v", err)
	}
	if sender.calls != 1 {
		t.Fatalf("duplicate event must not re-send, calls=%d", sender.calls)
	}
}

func TestOrchestratorExpiredRejected(t *testing.T) {
	h := newHarness(t, nil)
	payload := testPayload()
	payload.ExpiresAt = time.Now().Add(-time.Hour) // 已过期
	ctx := context.Background()
	if err := h.orch.Handle(ctx, payloadEvent(t, payload)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	n, _ := h.requests.Get(ctx, "req-1")
	if n.Status != domain.RequestRejected || !strings.Contains(n.RejectReason, "expired") {
		t.Fatalf("expired must be rejected: %+v", n)
	}
	// 拒发必须留审计。
	found := false
	for _, a := range h.emitter.actions() {
		if a == "notification.request.rejected" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reject must be audited, actions=%v", h.emitter.actions())
	}
}

func TestOrchestratorBlacklistBlocked(t *testing.T) {
	h := newHarness(t, nil)
	payload := testPayload()
	ctx := context.Background()
	// 平台级黑名单命中邮箱（HMAC 口径与编排管道一致）。
	if err := h.blacklist.Add(ctx, domain.Blacklist{
		TenantID: domain.PlatformTenantID, Channel: domain.ChannelEmail,
		TargetHMAC: hashTarget("bob@example.com"), Source: domain.BlacklistComplaint,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Add blacklist: %v", err)
	}
	if err := h.orch.Handle(ctx, payloadEvent(t, payload)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	n, _ := h.requests.Get(ctx, "req-1")
	if n.Status != domain.RequestRejected || !strings.Contains(n.RejectReason, "blacklisted") {
		t.Fatalf("blacklisted must be rejected: %+v", n)
	}
}

func TestOrchestratorPreferenceOptOut(t *testing.T) {
	h := newHarness(t, nil)
	payload := testPayload()
	payload.SceneClass = "marketing"
	ctx := context.Background()
	p, err := domain.NewPreference("t1", domain.SubjectUser, "user-1",
		domain.ClassMarketing, domain.ChannelEmail, false, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewPreference: %v", err)
	}
	if err := h.preferences.Upsert(ctx, p); err != nil {
		t.Fatalf("Upsert preference: %v", err)
	}
	if err := h.orch.Handle(ctx, payloadEvent(t, payload)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	n, _ := h.requests.Get(ctx, "req-1")
	if n.Status != domain.RequestRejected || !strings.Contains(n.RejectReason, "opted out") {
		t.Fatalf("opted-out must be rejected: %+v", n)
	}
}

func TestOrchestratorRetryableThenSuccess(t *testing.T) {
	var sender *fakeSender
	h := newHarness(t, func(_ *OrchestratorOptions, s map[string]*fakeSender) {
		sender = s["smtp"]
		sender.failN = 1
		sender.err = &SendError{Semantic: SemanticTimeout, Retryable: true, Summary: "dial timeout"}
	})
	ctx := context.Background()
	event := payloadEvent(t, testPayload())

	// 第一次：可重试失败 → 返回错误交 Outbox 退避（明细 retrying）。
	if err := h.orch.Handle(ctx, event); err == nil {
		t.Fatal("retryable failure must return error for outbox reschedule")
	}
	d, _ := h.deliveries.Get(ctx, "req-1", domain.ChannelEmail)
	if d.Status != domain.DeliveryRetrying || d.Attempt != 2 {
		t.Fatalf("delivery must be retrying with attempt=2: %+v", d)
	}
	n, _ := h.requests.Get(ctx, "req-1")
	if n.Status != domain.RequestDispatched {
		t.Fatalf("request must stay dispatched, got %s", n.Status)
	}

	// 第二次（重投）：续跑成功 → sent。
	if err := h.orch.Handle(ctx, event); err != nil {
		t.Fatalf("second Handle: %v", err)
	}
	d, _ = h.deliveries.Get(ctx, "req-1", domain.ChannelEmail)
	if d.Status != domain.DeliveryAccepted {
		t.Fatalf("delivery must be accepted, got %s", d.Status)
	}
	n, _ = h.requests.Get(ctx, "req-1")
	if n.Status != domain.RequestSent {
		t.Fatalf("request must converge to sent, got %s", n.Status)
	}
	if sender.calls != 2 {
		t.Fatalf("sender must be called twice, got %d", sender.calls)
	}
}

func TestOrchestratorBudgetExhaustedFails(t *testing.T) {
	var sender *fakeSender
	h := newHarness(t, func(o *OrchestratorOptions, s map[string]*fakeSender) {
		sender = s["smtp"]
		sender.failN = 99
		sender.err = &SendError{Semantic: SemanticTimeout, Retryable: true, Summary: "dial timeout"}
		payload := testPayload()
		_ = payload
		o.MaxAttempts = map[domain.Priority]int{domain.PriorityNormal: 2}
	})
	ctx := context.Background()
	event := payloadEvent(t, testPayload())

	// 反复重投直到预算耗尽；耗尽后必须收敛 failed 并吸收事件。
	for i := 0; i < 10; i++ {
		if err := h.orch.Handle(ctx, event); err == nil {
			break
		}
	}
	n, _ := h.requests.Get(ctx, "req-1")
	if n.Status != domain.RequestFailed {
		t.Fatalf("budget exhausted must converge to failed, got %s", n.Status)
	}
	d, _ := h.deliveries.Get(ctx, "req-1", domain.ChannelEmail)
	if d.Status != domain.DeliveryFailed || d.SemanticCode != SemanticTimeout {
		t.Fatalf("delivery must be failed with semantic code: %+v", d)
	}
}

func TestOrchestratorPermanentErrorNoRetry(t *testing.T) {
	var sender *fakeSender
	h := newHarness(t, func(_ *OrchestratorOptions, s map[string]*fakeSender) {
		sender = s["smtp"]
		sender.failN = 99
		sender.err = &SendError{Semantic: SemanticInvalidAddress, Summary: "invalid rcpt", ProviderCode: "550"}
	})
	// 永久错误：不重试，但事件必须以 retry.Permanent 进 DLQ（Replay 数据源）。
	err := h.orch.Handle(context.Background(), payloadEvent(t, testPayload()))
	if err == nil || !retry.IsPermanent(err) {
		t.Fatalf("permanent failure must surface as retry.Permanent, got %v", err)
	}
	n, _ := h.requests.Get(context.Background(), "req-1")
	if n.Status != domain.RequestFailed {
		t.Fatalf("permanent error must converge to failed, got %s", n.Status)
	}
	if sender.calls != 1 {
		t.Fatalf("permanent error must not retry, calls=%d", sender.calls)
	}
}

func TestOrchestratorFailoverOnOpenBreaker(t *testing.T) {
	var primary, backup *fakeSender
	var openBreaker *circuitbreaker.Breaker
	h := newHarness(t, func(o *OrchestratorOptions, s map[string]*fakeSender) {
		primary, backup = s["smtp"], s["ses"]
		primary.failN = 99
		primary.err = &SendError{Semantic: SemanticProviderError, Retryable: true, Summary: "5xx"}
		// 构造已 Open 的主供应商熔断器：首错即 trip。
		openBreaker = circuitbreaker.MustNew(circuitbreaker.Config{
			Name:        "notify-smtp",
			ReadyToTrip: func(circuitbreaker.Counts) bool { return true },
		})
		_ = openBreaker.Do(func() error { return errors.New("trip") }) // 触发 Open
		o.Breakers = map[string]*circuitbreaker.Breaker{"smtp": openBreaker}
		o.Route = map[domain.Channel][]Sender{domain.ChannelEmail: {primary, backup}}
	})
	if err := h.orch.Handle(context.Background(), payloadEvent(t, testPayload())); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if backup.calls != 1 {
		t.Fatalf("backup provider must be used, calls=%d", backup.calls)
	}
	d, _ := h.deliveries.Get(context.Background(), "req-1", domain.ChannelEmail)
	if d.Provider != "ses" {
		t.Fatalf("delivery must record actual provider, got %s", d.Provider)
	}
}

func TestOrchestratorRateLimited(t *testing.T) {
	h := newHarness(t, func(o *OrchestratorOptions, _ map[string]*fakeSender) {
		// 零配额滑动窗口：所有请求都被限流。
		limiter, err := ratelimit.NewKeyed(func() (ratelimit.Limiter, error) {
			return ratelimit.NewSlidingWindow(0, time.Minute)
		})
		if err != nil {
			t.Fatalf("NewKeyed: %v", err)
		}
		o.Limiter = limiter
	})
	err := h.orch.Handle(context.Background(), payloadEvent(t, testPayload()))
	if err == nil {
		t.Fatal("rate limited must return error for retry")
	}
	if h.observer.rateHits == 0 {
		t.Fatal("rate limited must be observed")
	}
}

func TestOrchestratorPoisonPayloadIsPermanent(t *testing.T) {
	h := newHarness(t, nil)
	// 非法载荷：毒消息必须 retry.Permanent（进 DLQ 而非无限重试）。
	if err := h.orch.Handle(context.Background(), &outbox.Event{Topic: TopicNotification}); err == nil || !retry.IsPermanent(err) {
		t.Fatalf("nil payload must be permanent, got %v", err)
	}
	bad := testPayload()
	bad.SchemaVersion = 99
	if err := h.orch.Handle(context.Background(), payloadEvent(t, bad)); err == nil || !retry.IsPermanent(err) {
		t.Fatalf("unknown schema must be permanent, got %v", err)
	}
	// 缺幂等键同样毒消息。
	noKey := testPayload()
	noKey.IdempotencyKey = ""
	if err := h.orch.Handle(context.Background(), payloadEvent(t, noKey)); err == nil || !retry.IsPermanent(err) {
		t.Fatalf("missing idempotency key must be permanent, got %v", err)
	}
}

func TestOutboxDispatcherAppend(t *testing.T) {
	store := &recorder{}
	dispatcher, err := NewOutboxDispatcher(store)
	if err != nil {
		t.Fatalf("NewOutboxDispatcher: %v", err)
	}
	payload := testPayload()
	if err := dispatcher.Dispatch(context.Background(), payload); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(store.events) != 1 {
		t.Fatalf("one event expected, got %d", len(store.events))
	}
	event := store.events[0]
	if event.Topic != TopicNotification || event.EventID != "req-1" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.IdempotencyKey != payload.IdempotencyKey {
		t.Fatalf("idempotency key must be carried: %s", event.IdempotencyKey)
	}
	// 载荷回读一致。
	var roundTrip RequestPayload
	if err := json.Unmarshal(event.Payload, &roundTrip); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if roundTrip.Scene != payload.Scene || roundTrip.Recipient.Email != payload.Recipient.Email {
		t.Fatalf("payload roundtrip mismatch: %+v", roundTrip)
	}
	// Store 未配置必须失败（禁止静默丢弃）。
	if _, err := NewOutboxDispatcher(nil); err == nil {
		t.Fatal("nil store must be rejected")
	}
	// 载荷缺关键字段必须 400。
	bad := payload
	bad.Scene = ""
	if err := dispatcher.Dispatch(context.Background(), bad); err == nil {
		t.Fatal("invalid payload must be rejected")
	}
}
