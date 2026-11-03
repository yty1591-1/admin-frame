package domain_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// fixedNow 固定时间，避免用例依赖真实时钟。
var fixedNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

// TestStatusTransitions 穷举状态机的合法与非法迁移。
func TestStatusTransitions(t *testing.T) {
	cases := []struct {
		from   domain.Status
		to     domain.Status
		allow  bool
		reason string
	}{
		{domain.StatusPending, domain.StatusActive, true, "开通"},
		{domain.StatusPending, domain.StatusSuspended, false, "未开通不允许直接停用"},
		{domain.StatusPending, domain.StatusTerminated, true, "开通前撤销"},
		{domain.StatusActive, domain.StatusSuspended, true, "停用"},
		{domain.StatusActive, domain.StatusTerminated, true, "终止"},
		{domain.StatusActive, domain.StatusPending, false, "不允许回退到未开通"},
		{domain.StatusSuspended, domain.StatusActive, true, "恢复"},
		{domain.StatusSuspended, domain.StatusPending, false, "不允许回退到未开通"},
		{domain.StatusTerminated, domain.StatusActive, false, "终态不可复活"},
		{domain.StatusTerminated, domain.StatusSuspended, false, "终态不可复活"},
	}
	for _, tc := range cases {
		t.Run(string(tc.from)+"->"+string(tc.to), func(t *testing.T) {
			if got := tc.from.CanTransitionTo(tc.to); got != tc.allow {
				t.Fatalf("CanTransitionTo(%s->%s) = %v, want %v", tc.from, tc.to, got, tc.allow)
			}

			tenant := domain.Tenant{ID: "t1", Name: "示例租户", Status: tc.from, Version: 3, CreatedAt: fixedNow, UpdatedAt: fixedNow}
			next, err := tenant.Transition(tc.to, tc.reason, fixedNow.Add(time.Minute))
			if !tc.allow {
				if err == nil {
					t.Fatalf("Transition(%s->%s) error = nil, want error", tc.from, tc.to)
				}
				// 失败时返回原实体：调用方可以继续用原值判错，而不是拿到零值。
				if next.Status != tc.from || next.Version != tenant.Version {
					t.Fatalf("非法迁移不得改变实体，got status=%s version=%d", next.Status, next.Version)
				}
				if tc.from.Terminal() {
					if !errors.Is(err, domain.ErrTerminated) {
						t.Fatalf("终态迁移 error = %v, want ErrTerminated", err)
					}
					return
				}
				var invalid *domain.InvalidTransitionError
				if !errors.As(err, &invalid) {
					t.Fatalf("Transition() error = %v, want InvalidTransitionError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Transition() error = %v", err)
			}
			if next.Status != tc.to {
				t.Fatalf("Status = %s, want %s", next.Status, tc.to)
			}
			if next.Version != tenant.Version+1 {
				t.Fatalf("Version = %d, want %d", next.Version, tenant.Version+1)
			}
			if next.StatusReason != tc.reason {
				t.Fatalf("StatusReason = %q, want %q", next.StatusReason, tc.reason)
			}
			switch tc.to {
			case domain.StatusSuspended:
				if next.SuspendedAt == nil {
					t.Fatal("停用必须记录 suspended_at")
				}
			case domain.StatusTerminated:
				if next.TerminatedAt == nil {
					t.Fatal("终止必须记录 terminated_at")
				}
			}
		})
	}
}

// TestTransitionRequiresReason 验证停用 / 终止必须给原因，开通与恢复不必。
func TestTransitionRequiresReason(t *testing.T) {
	active := domain.Tenant{ID: "t1", Name: "acme", Status: domain.StatusActive, Version: 1, CreatedAt: fixedNow, UpdatedAt: fixedNow}
	if _, err := active.Transition(domain.StatusSuspended, "   ", fixedNow); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("停用缺原因 error = %v, want ErrReasonRequired", err)
	}
	if _, err := active.Transition(domain.StatusTerminated, "", fixedNow); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("终止缺原因 error = %v, want ErrReasonRequired", err)
	}

	suspended := domain.Tenant{ID: "t1", Name: "acme", Status: domain.StatusSuspended, Version: 2, CreatedAt: fixedNow, UpdatedAt: fixedNow}
	if _, err := suspended.Transition(domain.StatusActive, "", fixedNow); err != nil {
		t.Fatalf("恢复不应要求原因，error = %v", err)
	}
	pending := domain.Tenant{ID: "t2", Name: "beta", Status: domain.StatusPending, Version: 1, CreatedAt: fixedNow, UpdatedAt: fixedNow}
	if _, err := pending.Transition(domain.StatusActive, "", fixedNow); err != nil {
		t.Fatalf("开通不应要求原因，error = %v", err)
	}
}

// TestTransitionOnTerminatedReturnsErrTerminated 验证终态先于状态机判定，
// 给出「已终止」而不是「不支持该迁移」，便于运营判断。
func TestTransitionOnTerminatedReturnsErrTerminated(t *testing.T) {
	moment := fixedNow
	tenant := domain.Tenant{
		ID: "t1", Name: "acme", Status: domain.StatusTerminated, Version: 4,
		TerminatedAt: &moment, CreatedAt: fixedNow, UpdatedAt: fixedNow,
	}
	if _, err := tenant.Transition(domain.StatusActive, "复活", fixedNow); !errors.Is(err, domain.ErrTerminated) {
		t.Fatalf("error = %v, want ErrTerminated", err)
	}
}

// TestNewTenantTrimsAndDefaults 验证创建即 pending、名称去空白、时间转 UTC。
func TestNewTenantTrimsAndDefaults(t *testing.T) {
	tenant, err := domain.NewTenant("t1", "  示例租户  ", fixedNow)
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	if tenant.Name != "示例租户" {
		t.Fatalf("Name = %q, want trimmed", tenant.Name)
	}
	if tenant.Status != domain.StatusPending {
		t.Fatalf("Status = %s, want pending", tenant.Status)
	}
	if tenant.Version != 1 {
		t.Fatalf("Version = %d, want 1", tenant.Version)
	}
	if _, err := domain.NewTenant("t1", "  ", fixedNow); !errors.Is(err, domain.ErrNameRequired) {
		t.Fatalf("空名称 error = %v, want ErrNameRequired", err)
	}
	longName := strings.Repeat("租", domain.MaxNameLength+1)
	if _, err := domain.NewTenant("t1", longName, fixedNow); !errors.Is(err, domain.ErrNameTooLong) {
		t.Fatalf("超长名称 error = %v, want ErrNameTooLong", err)
	}
}

// TestTenantValidateRejectsInconsistent 验证脏数据不会被当作正常状态继续流转。
func TestTenantValidateRejectsInconsistent(t *testing.T) {
	cases := map[string]domain.Tenant{
		"缺ID":   {Name: "acme", Status: domain.StatusActive, Version: 1},
		"缺名称":   {ID: "t1", Status: domain.StatusActive, Version: 1},
		"非法状态":  {ID: "t1", Name: "acme", Status: "frozen", Version: 1},
		"终态缺时间": {ID: "t1", Name: "acme", Status: domain.StatusTerminated, Version: 2},
		"版本非正数": {ID: "t1", Name: "acme", Status: domain.StatusActive, Version: 0},
	}
	for name, tenant := range cases {
		t.Run(name, func(t *testing.T) {
			if err := tenant.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}

// TestServingOnlyActive 验证只有 active 允许业务访问。
func TestServingOnlyActive(t *testing.T) {
	if !domain.StatusActive.Serving() {
		t.Fatal("active 应允许访问")
	}
	for _, status := range []domain.Status{domain.StatusPending, domain.StatusSuspended, domain.StatusTerminated} {
		if status.Serving() {
			t.Fatalf("%s 不应允许访问", status)
		}
	}
}

// TestFilterNormalize 验证分页默认值与关键字去空白。
func TestFilterNormalize(t *testing.T) {
	got := domain.Filter{Keyword: "  acme  ", Limit: 0, Offset: -3}.Normalize()
	if got.Keyword != "acme" {
		t.Fatalf("Keyword = %q", got.Keyword)
	}
	if got.Limit != 20 {
		t.Fatalf("Limit = %d, want 20", got.Limit)
	}
	if got.Offset != 0 {
		t.Fatalf("Offset = %d, want 0", got.Offset)
	}
}

// TestRepositoryContractIsSatisfied 编译期断言：内存实现满足仓储契约。
//
// 放在 domain 包测试里是为了让「改了接口但忘了改实现」在单测阶段就暴露。
func TestRepositoryContractIsSatisfied(_ *testing.T) {
	var _ domain.Repository = memoryStub{}
	_ = context.Background()
}

// memoryStub 只用于接口断言，不承载行为。
type memoryStub struct{}

func (memoryStub) Create(context.Context, domain.Tenant) error { return nil }
func (memoryStub) Get(context.Context, string) (domain.Tenant, error) {
	return domain.Tenant{}, nil
}
func (memoryStub) List(context.Context, domain.Filter) ([]domain.Tenant, int, error) {
	return nil, 0, nil
}
func (memoryStub) UpdateStatus(context.Context, domain.Tenant) error            { return nil }
func (memoryStub) UpdateProfile(context.Context, domain.Tenant) error           { return nil }
func (memoryStub) CountByStatus(context.Context) (map[domain.Status]int, error) { return nil, nil }

// WithinTx 在测试中直接执行 fn（无事务语义）。
func (memoryStub) WithinTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

// TestNormalizeCode 验证业务编码规范化与校验（T-11）。
func TestNormalizeCode(t *testing.T) {
	normalized, err := domain.NormalizeCode("  acme-cn  ")
	if err != nil || normalized != "acme-cn" {
		t.Fatalf("NormalizeCode = %q, %v", normalized, err)
	}
	for name, code := range map[string]string{
		"空值":   "   ",
		"超长":   strings.Repeat("a", domain.MaxCodeLength+1),
		"大写":   "Acme",
		"空格":   "bad code",
		"特殊字符": "bad/code",
	} {
		if _, err := domain.NormalizeCode(code); err == nil {
			t.Fatalf("%s 应返回错误", name)
		}
	}
}

// TestNewTenantDefaultsCodeToID 验证编码兜底策略：code = id（与迁移 000013 同源）。
func TestNewTenantDefaultsCodeToID(t *testing.T) {
	tenant, err := domain.NewTenant("19700001", "示例租户", fixedNow)
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	if tenant.Code != "19700001" {
		t.Fatalf("Code = %q, want 19700001", tenant.Code)
	}
}

// TestWithProfile 验证资料变更：字段合并、版本递增、清空语义与格式校验。
func TestWithProfile(t *testing.T) {
	tenant, err := domain.NewTenant("t1", "acme", fixedNow)
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}

	next, err := tenant.WithProfile(domain.Profile{
		Name: "acme-cn", ContactName: " 张三 ", ContactEmail: "ops@acme.com",
		Industry: "制造", Timezone: "Asia/Shanghai", Locale: "zh-CN", ExternalID: "crm-42",
	}, fixedNow)
	if err != nil {
		t.Fatalf("WithProfile() error = %v", err)
	}
	if next.Version != tenant.Version+1 {
		t.Fatalf("Version = %d, want %d", next.Version, tenant.Version+1)
	}
	if next.ContactName != "张三" {
		t.Fatalf("联系人应去空白: %q", next.ContactName)
	}

	// 空串 = 清空；WithProfile 是全量语义，「不变」字段的合并由应用层负责，
	// 这里显式带上要保留的值。
	cleared, err := next.WithProfile(domain.Profile{
		Name: "acme-cn", ContactName: "张三", ContactEmail: " ",
		Industry: "制造", Timezone: "Asia/Shanghai", Locale: "zh-CN", ExternalID: "crm-42",
	}, fixedNow)
	if err != nil {
		t.Fatalf("WithProfile(clear) error = %v", err)
	}
	if cleared.ContactEmail != "" {
		t.Fatalf("空串应清空邮箱, got %q", cleared.ContactEmail)
	}
	if cleared.ContactName != "张三" || cleared.Industry != "制造" || cleared.ExternalID != "crm-42" {
		t.Fatalf("其余字段应保留: %+v", cleared)
	}

	// 校验失败不产生半变更。
	failures := []struct {
		name    string
		profile domain.Profile
	}{
		{"空名称", domain.Profile{Name: "   "}},
		{"超长名称", domain.Profile{Name: strings.Repeat("名", domain.MaxNameLength+1)}},
		{"无 @ 邮箱", domain.Profile{Name: "x", ContactEmail: "ops.acme.com"}},
		{"带空格邮箱", domain.Profile{Name: "x", ContactEmail: "ops @acme.com"}},
		{"超长邮箱", domain.Profile{Name: "x", ContactEmail: strings.Repeat("a", domain.MaxContactEmailLength) + "@x.com"}},
		{"非法时区", domain.Profile{Name: "x", Timezone: "Asia/Shanghai;drop"}},
		{"非法语言", domain.Profile{Name: "x", Locale: "zh CN"}},
		{"超长外部ID", domain.Profile{Name: "x", ExternalID: strings.Repeat("e", domain.MaxExternalIDLength+1)}},
	}
	for _, tc := range failures {
		if _, err := tenant.WithProfile(tc.profile, fixedNow); err == nil {
			t.Fatalf("%s 应返回错误", tc.name)
		}
	}
}

// TestTransitionMatrixSnapshot 把 transitions 白名单以硬编码快照锁定（T-17）。
//
// 迁移矩阵是「唯一真相」，被守卫、登录校验、开通前置共同消费——悄悄改它
// 的语义影响面远超一个单测：本快照强制任何白名单变更必须显式修改此处，
// 让 diff 可见、评审可议。变更流程：改 transitions → 跑本测试看 diff →
// 确认业务语义后更新快照 → 在 ADR-0006 记录理由。
func TestTransitionMatrixSnapshot(t *testing.T) {
	// 按「from: to1,to2」排序序列化，保证快照稳定。
	lines := make([]string, 0, 4)
	for from, targets := range domain.Transitions() {
		sort.Slice(targets, func(i, j int) bool { return targets[i] < targets[j] })
		parts := make([]string, 0, len(targets))
		for _, to := range targets {
			parts = append(parts, string(to))
		}
		lines = append(lines, string(from)+": "+strings.Join(parts, ","))
	}
	sort.Strings(lines)
	got := strings.Join(lines, "\n")

	// 2026-11 快照（T-17 定格）。suspended 不允许回到 pending；terminated 为终态。
	const want = `active: suspended,terminated
pending: active,terminated
suspended: active,terminated`
	if got != want {
		t.Fatalf("迁移矩阵已变更，更新此快照前先评审语义：\n got:/n%s/nwant:/n%s", got, want)
	}
}
