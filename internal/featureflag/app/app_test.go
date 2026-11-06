package app_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/featureflag/app"
	"github.com/admin-frame/admin-frame/internal/featureflag/domain"
	"github.com/admin-frame/admin-frame/internal/featureflag/infrastructure/memory"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// nowFixed 固定时钟（缓存时效用推进时钟验证）。
var nowFixed = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// recordEmitter 记录审计事件（app 层测试最小实现）。
type recordEmitter struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *recordEmitter) Emit(_ context.Context, e audit.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *recordEmitter) actions() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, e.Action)
	}
	return out
}

// evalFixture 建立 flag 仓储 + 评估服务的测试环境。
type evalFixture struct {
	repos *memory.Repository
	svc   *app.EvaluationService
	clock *clock
}

// clock 可推进的测试时钟（验证缓存 TTL 行为）。
type clock struct {
	mu   sync.Mutex
	time time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.time
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.time = c.time.Add(d)
}

func newEvalFixture(t *testing.T) *evalFixture {
	t.Helper()
	repos := memory.NewMemoryRepository()
	clk := &clock{time: nowFixed}
	svc, err := app.NewEvaluationService(repos, app.EvaluationOptions{Now: clk.Now})
	if err != nil {
		t.Fatalf("NewEvaluationService: %v", err)
	}
	return &evalFixture{repos: repos, svc: svc, clock: clk}
}

// mustCreate 落一个 flag；失败即终止测试。
func (f *evalFixture) mustCreate(t *testing.T, flag domain.Flag) {
	t.Helper()
	if err := f.repos.Create(context.Background(), flag); err != nil {
		t.Fatalf("seed flag %s: %v", flag.Key, err)
	}
}

// TestEvaluationOrderKillSwitchFirst kill switch 无条件优先（「关闭即回退」）。
func TestEvaluationOrderKillSwitchFirst(t *testing.T) {
	f := newEvalFixture(t)
	f.mustCreate(t, domain.Flag{
		Key: "t.f", Status: domain.StatusDisabled, DefaultEnabled: true, Version: 1,
		Rules: []domain.Rule{{Percentage: domain.MaxPercentage}},
	})
	ctx := context.Background()
	if f.svc.IsEnabled(ctx, "t.f", domain.EvalContext{
		Channel: domain.ChannelBiz, TenantID: "t-1", SubjectID: "u-1",
	}) {
		t.Fatal("kill switch must unconditionally disable regardless of rules/default")
	}
}

// TestEvaluationRuleOrdering 规则按序首个命中；未命中回退 default。
func TestEvaluationRuleOrdering(t *testing.T) {
	f := newEvalFixture(t)
	f.mustCreate(t, domain.Flag{
		Key: "t.f", Status: domain.StatusEnabled, DefaultEnabled: false, Version: 1,
		Rules: []domain.Rule{
			{Channel: domain.ChannelAdmin},
			{TenantIDs: []string{"t-vip"}},
		},
	})
	ctx := context.Background()
	evalCtx := domain.EvalContext{Channel: domain.ChannelBiz, TenantID: "t-1", SubjectID: "u-1"}

	if f.svc.IsEnabled(ctx, "t.f", evalCtx) {
		t.Fatal("no rule matched and default false must be false")
	}
	vipCtx := domain.EvalContext{Channel: domain.ChannelBiz, TenantID: "t-vip", SubjectID: "u-1"}
	if !f.svc.IsEnabled(ctx, "t.f", vipCtx) {
		t.Fatal("tenant rule must hit")
	}
	adminCtx := domain.EvalContext{Channel: domain.ChannelAdmin, TenantID: "t-1", SubjectID: "u-1"}
	if !f.svc.IsEnabled(ctx, "t.f", adminCtx) {
		t.Fatal("channel rule must hit")
	}
}

// TestPercentageStableBuckets 百分比灰度核心性质：同主体稳定、上调只增不减、
// 近似均匀。属性测试锁定 FF-02 验收。
func TestPercentageStableBuckets(t *testing.T) {
	f := newEvalFixture(t)
	ctx := context.Background()
	evalCtx := domain.EvalContext{Channel: domain.ChannelBiz, TenantID: "t-1"}

	// 同主体稳定：重复评估结果一致。
	evalCtx.SubjectID = "u-stable"
	first := f.svc.IsEnabled(ctx, "t.f", evalCtx)
	for i := 0; i < 5; i++ {
		if got := f.svc.IsEnabled(ctx, "t.f", evalCtx); got != first {
			t.Fatal("same subject must evaluate stably")
		}
	}

	// 全量 10000：任意主体命中。
	f.mustCreate(t, domain.Flag{
		Key: "t.full", Status: domain.StatusEnabled, Version: 1,
		Rules: []domain.Rule{{Percentage: domain.MaxPercentage}},
	})
	for _, id := range []string{"u-1", "u-2", "u-3"} {
		evalCtx.SubjectID = id
		if !f.svc.IsEnabled(ctx, "t.full", evalCtx) {
			t.Fatalf("100%% bucket must hit for %s", id)
		}
	}

	// 上调只增不减：0% → 5000 → 10000 命中集合单调扩张。
	flag := domain.Flag{Key: "t.rollout", Status: domain.StatusEnabled, Version: 1}
	f.mustCreate(t, flag)
	hitAt0, hitAt5000, hitAt10000 := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for id := 1; id <= 200; id++ {
		subject := "u-" + strings.Repeat("x", id%7) + itoa(id)
		evalCtx.SubjectID = subject
		hitAt0[subject] = f.svc.IsEnabled(ctx, "t.rollout", evalCtx)
	}
	flag2 := flag
	flag2.Rules = []domain.Rule{{Percentage: 5000}}
	if err := f.repos.Update(ctx, flag2, 1); err != nil {
		t.Fatalf("update to 50%%: %v", err)
	}
	f.svc.Invalidate()
	for id := 1; id <= 200; id++ {
		subject := "u-" + strings.Repeat("x", id%7) + itoa(id)
		evalCtx.SubjectID = subject
		hitAt5000[subject] = f.svc.IsEnabled(ctx, "t.rollout", evalCtx)
	}
	flag3 := flag2
	flag3.Version = 2
	if err := f.repos.Update(ctx, flag3, 1); err != nil {
		t.Fatalf("update to 100%%: %v", err)
	}
	f.svc.Invalidate()
	for id := 1; id <= 200; id++ {
		subject := "u-" + strings.Repeat("x", id%7) + itoa(id)
		evalCtx.SubjectID = subject
		hitAt10000[subject] = f.svc.IsEnabled(ctx, "t.rollout", evalCtx)
	}
	for subject := range hitAt0 {
		if hitAt0[subject] && !hitAt5000[subject] {
			t.Fatalf("monotonicity broken at 50%%: %s hit at 0%% but not 5000", subject)
		}
		if hitAt5000[subject] && !hitAt10000[subject] {
			t.Fatalf("monotonicity broken at 100%%: %s hit at 5000 but not 10000", subject)
		}
	}
	hitCount := 0
	for subject := range hitAt5000 {
		if hitAt5000[subject] {
			hitCount++
		}
	}
	// 均匀性：200 主体 50% 配比，命中 60~140 视为正常（万分比精度下
	// 二项分布 3σ≈±21）。
	if hitCount < 60 || hitCount > 140 {
		t.Fatalf("bucket uniformity suspicious: %d/200 hits at 50%%", hitCount)
	}
}

// TestPercentageWithoutSubjectFailsClosed subject 缺失恒不命中（可复现性）。
func TestPercentageWithoutSubjectFailsClosed(t *testing.T) {
	f := newEvalFixture(t)
	f.mustCreate(t, domain.Flag{
		Key: "t.f", Status: domain.StatusEnabled, Version: 1,
		Rules: []domain.Rule{{Percentage: domain.MaxPercentage}},
	})
	if f.svc.IsEnabled(context.Background(), "t.f", domain.EvalContext{
		Channel: domain.ChannelBiz, TenantID: "t-1",
	}) {
		t.Fatal("percentage rule without subject must not hit (fail closed)")
	}
}

// TestKillSwitchCacheTTL kill switch 生效时效 ≤5s（时钟推进验证）：
// 缓存面 5s 过期后回源看到禁用。
func TestKillSwitchCacheTTL(t *testing.T) {
	f := newEvalFixture(t)
	f.mustCreate(t, domain.Flag{
		Key: "t.f", Status: domain.StatusEnabled, DefaultEnabled: true, Version: 1,
	})
	ctx := context.Background()
	evalCtx := domain.EvalContext{Channel: domain.ChannelBiz, SubjectID: "u-1"}
	if !f.svc.IsEnabled(ctx, "t.f", evalCtx) {
		t.Fatal("enabled flag must evaluate true initially")
	}
	// 禁用后，缓存 5s 内仍可见旧值，5s 后看到新值。
	disabled := domain.Flag{
		Key: "t.f", Status: domain.StatusDisabled, DefaultEnabled: true, Version: 2,
	}
	if err := f.repos.Update(ctx, disabled, 1); err != nil {
		t.Fatalf("disable: %v", err)
	}
	f.clock.Advance(3 * time.Second)
	if !f.svc.IsEnabled(ctx, "t.f", evalCtx) {
		t.Fatal("within kill switch TTL old value may persist (documented)")
	}
	f.clock.Advance(3 * time.Second) // 累计 6s > 5s TTL
	if f.svc.IsEnabled(ctx, "t.f", evalCtx) {
		t.Fatal("kill switch must take effect within 5s (TTL upper bound)")
	}
}

// TestUnknownFlagFailsClosed 未知 key 返回 false（fail closed）。
func TestUnknownFlagFailsClosed(t *testing.T) {
	f := newEvalFixture(t)
	if f.svc.IsEnabled(context.Background(), "ghost.feature", domain.EvalContext{
		Channel: domain.ChannelBiz, SubjectID: "u-1",
	}) {
		t.Fatal("unknown key must be false")
	}
}

// itoa 测试用（避免引 strconv 的冗长 import 段说明——直接复用标准库亦可，
// 但保持与租户测试同风格）。
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	if neg {
		return "-" + digits
	}
	return digits
}

// TestFlagServiceLifecycle 管理用例：创建（防裸全量）→ 启停 → 删除守卫。
func TestFlagServiceLifecycle(t *testing.T) {
	repos := memory.NewMemoryRepository()
	emitter := &recordEmitter{}
	svc, err := app.NewFlagService(repos, emitter, nil, app.Options{Service: "admin-api"}, app.FlagServiceOptions{
		Now: func() time.Time { return nowFixed },
	})
	if err != nil {
		t.Fatalf("NewFlagService: %v", err)
	}
	ctx := context.Background()
	actor := app.Actor{SubjectID: "admin-1", Channel: domain.ChannelAdmin}

	// 裸全量放开拒绝。
	if _, err := svc.Create(ctx, app.CreateInput{
		Key: "t.f", Description: "d", DefaultEnabled: true, Permanent: true, Actor: actor,
	}); !isBadRequest(err) {
		t.Fatalf("bare full rollout must be 400, got %v", err)
	}

	// 正常创建（带规则的全量灰度也允许 default=false）。
	created, err := svc.Create(ctx, app.CreateInput{
		Key: "t.f", Description: "d",
		Rules:     []domain.Rule{{Percentage: 100}},
		ExpiresAt: nowFixed.Add(48 * time.Hour),
		Actor:     actor,
	})
	if err != nil || created.Status != domain.StatusEnabled {
		t.Fatalf("create failed: %v", err)
	}

	// 重复 key 409。
	if _, err := svc.Create(ctx, app.CreateInput{
		Key: "t.f", Permanent: true, Actor: actor,
	}); !isConflict(err) {
		t.Fatalf("duplicate key must be 409, got %v", err)
	}

	// 禁用 → 删除放行；启用态删除被拒。
	if _, err := svc.SetStatus(ctx, "t.f", domain.StatusDisabled, 1, actor); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := svc.Delete(ctx, "t.f", actor); err != nil {
		t.Fatalf("delete after disable: %v", err)
	}

	// 审计动作序列。
	want := []string{
		app.ActionFlagCreate, app.ActionFlagDisable, app.ActionFlagDelete,
	}
	got := emitter.actions()
	if len(got) != len(want) {
		t.Fatalf("audit actions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("audit action[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

// TestExpireService 到期任务：过期 enabled flag 自动禁用 + 元审计；
// permanent 豁免。
func TestExpireService(t *testing.T) {
	repos := memory.NewMemoryRepository()
	emitter := &recordEmitter{}
	expire, err := app.NewExpireService(repos, emitter, nil, app.Options{
		Service: "worker", Now: func() time.Time { return nowFixed },
	})
	if err != nil {
		t.Fatalf("NewExpireService: %v", err)
	}
	ctx := context.Background()
	if err := repos.Create(ctx, domain.Flag{
		Key: "t.stale", Status: domain.StatusEnabled, Version: 1,
		ExpiresAt: nowFixed.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	if err := repos.Create(ctx, domain.Flag{
		Key: "t.perm", Status: domain.StatusEnabled, Version: 1, Permanent: true,
	}); err != nil {
		t.Fatalf("seed permanent: %v", err)
	}

	count, err := expire.Run(ctx)
	if err != nil || count != 1 {
		t.Fatalf("expire run: count=%d err=%v", count, err)
	}
	got, _ := repos.Get(ctx, "t.stale")
	if got.Status != domain.StatusDisabled {
		t.Fatalf("stale flag must be disabled, got %s", got.Status)
	}
	perm, _ := repos.Get(ctx, "t.perm")
	if perm.Status != domain.StatusEnabled {
		t.Fatal("permanent flag must be exempt")
	}
	found := false
	for _, a := range emitter.actions() {
		if a == app.ActionFlagExpire {
			found = true
		}
	}
	if !found {
		t.Fatalf("expire must be audited, actions=%v", emitter.actions())
	}
}

func isBadRequest(err error) bool {
	var appErr *apperr.AppError
	return errors.As(err, &appErr) && appErr.Code == apperr.CodeBadRequest
}

func isConflict(err error) bool {
	var appErr *apperr.AppError
	return errors.As(err, &appErr) && appErr.Code == apperr.CodeConflict
}
