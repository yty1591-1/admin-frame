package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/notification/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// recordEmitter 记录审计事件，供断言动作、结果与 attrs（对齐租户域测试模式）。
type recordEmitter struct {
	mu     sync.Mutex
	events []audit.Event
	err    error
}

func (e *recordEmitter) Emit(_ context.Context, event audit.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	e.events = append(e.events, event)
	return nil
}

func (e *recordEmitter) actions() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.events))
	for _, ev := range e.events {
		out = append(out, ev.Action)
	}
	return out
}

func (e *recordEmitter) last() (audit.Event, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.events) == 0 {
		return audit.Event{}, false
	}
	return e.events[len(e.events)-1], true
}

func newIDGenerator(t *testing.T) id.Generator {
	t.Helper()
	gen, err := id.NewSnowflake(id.Options{NodeID: 1})
	if err != nil {
		t.Fatalf("NewSnowflake: %v", err)
	}
	return gen
}

func testActor() Actor {
	return Actor{SubjectID: "admin-1", ActorType: "user", Channel: "admin", MerchantID: "t1", TraceID: "trace-1"}
}

func TestTemplateServiceUpsertAndAudit(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryTemplateRepository()
	emitter := &recordEmitter{}
	svc, err := NewTemplateService(repo, newIDGenerator(t), emitter, Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewTemplateService: %v", err)
	}

	saved, err := svc.Upsert(ctx, UpsertTemplateInput{
		Key: "tenant.invitation", Channel: domain.ChannelEmail, Locale: "zh-CN",
		Subject: "邀请 {{tenant_name}}", Body: "你好 {{invitee}}，加入链接 {{invite_url}}",
		Actor: testActor(),
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if saved.Version != 1 {
		t.Fatalf("first version must be 1, got %d", saved.Version)
	}
	if actions := emitter.actions(); len(actions) != 1 || actions[0] != ActionTemplateCreate {
		t.Fatalf("audit actions: %v", actions)
	}

	// 同键重传 = 新版本 + update 动作。
	saved2, err := svc.Upsert(ctx, UpsertTemplateInput{
		Key: "tenant.invitation", Channel: domain.ChannelEmail, Locale: "zh-CN",
		Subject: "邀请 {{tenant_name}}", Body: "新正文 {{invitee}} {{invite_url}}",
		Actor: testActor(),
	})
	if err != nil {
		t.Fatalf("Upsert v2: %v", err)
	}
	if saved2.Version != 2 {
		t.Fatalf("second version must be 2, got %d", saved2.Version)
	}
	if actions := emitter.actions(); len(actions) != 2 || actions[1] != ActionTemplateUpdate {
		t.Fatalf("audit actions after update: %v", actions)
	}

	// 邮件模板缺主题必须拒绝（SMTP 裸主题等于垃圾邮件）。
	if _, err := svc.Upsert(ctx, UpsertTemplateInput{
		Key: "tenant.invitation", Channel: domain.ChannelEmail, Locale: "en",
		Body: "body {{a}}", Actor: testActor(),
	}); err == nil {
		t.Fatal("email template without subject must be rejected")
	}

	// 停用后渲染定位不到模板（404 语义）。
	if err := svc.SetEnabled(ctx, "tenant.invitation", domain.ChannelEmail, "zh-CN", false, testActor()); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if _, err := svc.Render(ctx, "tenant.invitation", domain.ChannelEmail, "zh-CN",
		map[string]string{"tenant_name": "a", "invitee": "b", "invite_url": "c"}); err == nil {
		t.Fatal("disabled template must not render")
	}
}

func TestTemplateServiceMissingParamRejected(t *testing.T) {
	ctx := context.Background()
	svc, err := NewTemplateService(persistence.NewMemoryTemplateRepository(), newIDGenerator(t),
		&recordEmitter{}, Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewTemplateService: %v", err)
	}
	if _, err := svc.Upsert(ctx, UpsertTemplateInput{
		Key: "tenant.invitation", Channel: domain.ChannelEmail, Locale: "zh-CN",
		Subject: "s {{a}}", Body: "b {{a}}", Actor: testActor(),
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// 缺参：400 而不是 500（调用方拼错键是输入错误）。
	if _, err := svc.Render(ctx, "tenant.invitation", domain.ChannelEmail, "zh-CN", map[string]string{}); err == nil {
		t.Fatal("missing param must be rejected")
	}
}

func TestPreferenceServiceTransactionalOptOutGuard(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryPreferenceRepository()
	emitter := &recordEmitter{}
	svc, err := NewPreferenceService(repo, newIDGenerator(t), emitter, Options{Service: "biz-api"})
	if err != nil {
		t.Fatalf("NewPreferenceService: %v", err)
	}

	// 事务类退订直接拒绝。
	_, err = svc.Set(ctx, SetPreferenceInput{
		TenantID: "t1", SubjectType: domain.SubjectUser, SubjectID: "u1",
		SceneClass: domain.ClassTransactional, Channel: domain.ChannelSms,
		Enabled: false, Actor: testActor(),
	})
	if err == nil {
		t.Fatal("transactional opt-out must be rejected")
	}
	if len(emitter.actions()) != 0 {
		t.Fatalf("rejected opt-out must not emit audit, got %v", emitter.actions())
	}

	// 营销类退订成功并留审计。
	if _, err := svc.Set(ctx, SetPreferenceInput{
		TenantID: "t1", SubjectType: domain.SubjectUser, SubjectID: "u1",
		SceneClass: domain.ClassMarketing, Channel: domain.ChannelEmail,
		Enabled: false, Actor: testActor(),
	}); err != nil {
		t.Fatalf("marketing opt-out: %v", err)
	}
	if actions := emitter.actions(); len(actions) != 1 || actions[0] != ActionPreferenceUpdate {
		t.Fatalf("audit actions: %v", actions)
	}

	// IsAllowed：退订后 false；未设置渠道 true（opt-in 缺省允许）。
	allowed, err := svc.IsAllowed(ctx, "t1", domain.SubjectUser, "u1", domain.ClassMarketing, domain.ChannelEmail)
	if err != nil || allowed {
		t.Fatalf("opted-out must be disallowed: allowed=%v err=%v", allowed, err)
	}
	allowed, err = svc.IsAllowed(ctx, "t1", domain.SubjectUser, "u1", domain.ClassOperational, domain.ChannelPush)
	if err != nil || !allowed {
		t.Fatalf("default must be allowed: allowed=%v err=%v", allowed, err)
	}
}

func TestBlacklistServiceHMACAndAudit(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryBlacklistRepository()
	emitter := &recordEmitter{}
	hasher, err := HMACHasher([]byte("test-salt"))
	if err != nil {
		t.Fatalf("HMACHasher: %v", err)
	}
	svc, err := NewBlacklistService(repo, hasher, newIDGenerator(t), emitter, Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewBlacklistService: %v", err)
	}

	b, err := svc.Add(ctx, AddBlacklistInput{
		TenantID: domain.PlatformTenantID, Channel: domain.ChannelEmail,
		Target: "spam@example.com", Source: domain.BlacklistComplaint,
		Reason: "user complaint", Actor: testActor(),
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(b.TargetHMAC) != 64 || b.TargetHMAC == "spam@example.com" {
		t.Fatalf("target must be stored as hmac, got %q", b.TargetHMAC)
	}
	if actions := emitter.actions(); len(actions) != 1 || actions[0] != ActionBlacklistAdd {
		t.Fatalf("audit actions: %v", actions)
	}
	// 审计 attrs 不落明文目标。
	if last, ok := emitter.last(); ok {
		if v, exists := last.Attrs["target"]; exists && v == "spam@example.com" {
			t.Fatal("plaintext target must not appear in audit attrs")
		}
	}

	// 平台级命中所有租户查询。
	blocked, err := svc.IsBlocked(ctx, "tenant-1", domain.ChannelEmail, "spam@example.com")
	if err != nil || !blocked {
		t.Fatalf("platform blacklist must block: blocked=%v err=%v", blocked, err)
	}
	if blocked, _ := svc.IsBlocked(ctx, "tenant-1", domain.ChannelEmail, "other@example.com"); blocked {
		t.Fatal("unrelated target must not be blocked")
	}

	// 移除并留审计。
	if err := svc.Remove(ctx, domain.PlatformTenantID, domain.ChannelEmail, b.TargetHMAC, testActor()); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if actions := emitter.actions(); len(actions) != 2 || actions[1] != ActionBlacklistRemove {
		t.Fatalf("audit actions after remove: %v", actions)
	}
	if blocked, _ := svc.IsBlocked(ctx, "tenant-1", domain.ChannelEmail, "spam@example.com"); blocked {
		t.Fatal("removed entry must not block")
	}

	// 无盐 hasher 构造必须失败（无盐 = 可离线枚举）。
	if _, err := HMACHasher(nil); err == nil {
		t.Fatal("empty salt must be rejected")
	}
	// 空目标必须拒绝。
	if _, err := svc.Add(ctx, AddBlacklistInput{
		TenantID: "t1", Channel: domain.ChannelEmail, Target: " ",
		Source: domain.BlacklistManual, Actor: testActor(),
	}); err == nil {
		t.Fatal("empty target must be rejected")
	}
}

func TestTranslateDomainErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int // 期望稳定业务数字码（apperr.Code*）
	}{
		{"not found", domain.ErrNotFound, apperr.CodeNotFound},
		{"template not found", domain.ErrTemplateNotFound, apperr.CodeNotFound},
		{"duplicate request", domain.ErrDuplicateRequest, apperr.CodeConflict},
		{"rate limited", domain.ErrRateLimited, apperr.CodeTooManyRequests},
		{"channel unavailable", domain.ErrChannelUnavailable, apperr.CodeServiceUnavailable},
		{"transactional opt-out", domain.ErrTransactionalOptOut, apperr.CodeBadRequest},
		{"version conflict", domain.ErrVersionConflict, apperr.CodeConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := translateIfNeeded(tc.err)
			var appErr *apperr.AppError
			if !errors.As(got, &appErr) {
				t.Fatalf("want AppError, got %T", got)
			}
			if appErr.NumericCode() != tc.want {
				t.Fatalf("numeric code: got %d, want %d", appErr.NumericCode(), tc.want)
			}
		})
	}
	// 已翻译的 apperr 必须原样透传（不二次包装）。
	wrapped := apperr.BadRequest("already translated")
	if got := translateIfNeeded(wrapped); got != wrapped {
		t.Fatalf("AppError must pass through, got %v", got)
	}
}
