package app_test

// 清单生成的两处性能契约（S7-2 性能实测的回归锚点）。
//
// 只靠集成层的耗时数字无法在 CI 里守住：耗时会随机器浮动，且性能用例默认跳过。
// 这里把「按角色去重」与「批量落库」转成确定性断言，任何一次退回逐条实现都会红。

import (
	"context"
	"sync"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
)

// countingRoleRepo 统计 FindByID 调用次数。
type countingRoleRepo struct {
	*persistence.MemoryRoleRepository
	mu      sync.Mutex
	lookups int
}

func (r *countingRoleRepo) FindByID(ctx context.Context, id string) (*domain.Role, error) {
	r.mu.Lock()
	r.lookups++
	r.mu.Unlock()
	return r.MemoryRoleRepository.FindByID(ctx, id)
}

func (r *countingRoleRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lookups
}

// TestCertificationActivateCachesPrivilegedLookupPerRole 锁定「特权判定按角色去重」。
//
// 背景：SQL 仓储的 FindByID 一次要 3 条查询（角色 + 权限 + 数据范围），
// 按绑定逐条判定时 1 万绑定 = 3 万次往返（实测 49.5s）。去重后同规模降到 3 次。
// 断言用「1 个角色 3 条绑定 → 只查 1 次」，与规模无关，适合放在单测里守。
func TestCertificationActivateCachesPrivilegedLookupPerRole(t *testing.T) {
	f := newFixture(t)
	// iam: 前缀权限 = 高危角色，PrivilegedOnly 不会把它过滤掉。
	role := createCertRole(t, f, "ops_cert_priv_cache", iam.PermRoleWrite)
	for _, subject := range []string{"3001", "3002", "3003"} {
		bindCertSubject(t, f, subject, role.ID)
	}

	repo := &countingRoleRepo{MemoryRoleRepository: f.roles}
	svc, err := app.NewCertificationService(app.CertificationServiceConfig{
		Campaigns:  f.certCampaigns,
		Tasks:      f.certTasks,
		Bindings:   f.bindings,
		Roles:      repo,
		Revoker:    f.certRevoker,
		Recorder:   f.recorder,
		UnitOfWork: f.uow,
		IDs:        &seqIDs{},
		Audit:      app.AuditMeta{Service: "test", ActorType: "staff"},
		Limits: app.CertificationLimits{
			RequireReason:   true,
			AllowBizChannel: true,
		},
	})
	if err != nil {
		t.Fatalf("NewCertificationService: %v", err)
	}

	campaign := createCertCampaign(t, f, []string{role.ID}, true)
	if _, err := svc.ActivateCampaign(context.Background(), campaign.ID, certScope(), "1001"); err != nil {
		t.Fatalf("ActivateCampaign: %v", err)
	}

	if got := repo.count(); got != 1 {
		t.Fatalf("role lookups=%d, want 1（同角色多绑定必须只判定一次，逐条判定会随绑定数线性放大）", got)
	}
	// 去重只允许减少查询次数，不得改变生成结果。
	tasks, total, err := svc.ListTasks(context.Background(), campaign.ID, certScope(),
		domain.CertificationTaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if total != 3 || len(tasks) != 3 {
		t.Fatalf("tasks=%d total=%d, want 3（去重不得改变清单内容）", len(tasks), total)
	}
}

// countingTaskRepo 统计 SaveAll 被调用时每批的行数分布。
type countingTaskRepo struct {
	*persistence.MemoryCertificationTaskRepository
	mu      sync.Mutex
	batches []int
}

func (r *countingTaskRepo) SaveAll(ctx context.Context, tasks []*domain.CertificationTask) error {
	r.mu.Lock()
	r.batches = append(r.batches, len(tasks))
	r.mu.Unlock()
	return r.MemoryCertificationTaskRepository.SaveAll(ctx, tasks)
}

func (r *countingTaskRepo) batchSizes() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.batches...)
}

// TestCertificationActivateSavesTasksInOneBatch 锁定「清单一次批量落库」。
//
// 实现契约：generateTasks 产出的切片必须整体交给 SaveAll 一次（由仓储按批切分），
// 而不是在应用层逐条调用。若未来有人把 SaveAll 拆成循环，这里会立刻失败——
// 内存仓储没有 SQL 往返，耗时看不出来，但语句数的语义变化是真问题。
func TestCertificationActivateSavesTasksInOneBatch(t *testing.T) {
	f := newFixture(t)
	role := createCertRole(t, f, "ops_cert_bulk_save", iam.PermRoleRead)
	subjects := []string{"3101", "3102", "3103", "3104", "3105"}
	for _, subject := range subjects {
		bindCertSubject(t, f, subject, role.ID)
	}

	repo := &countingTaskRepo{MemoryCertificationTaskRepository: f.certTasks}
	svc, err := app.NewCertificationService(app.CertificationServiceConfig{
		Campaigns:  f.certCampaigns,
		Tasks:      repo,
		Bindings:   f.bindings,
		Roles:      f.roles,
		Revoker:    f.certRevoker,
		Recorder:   f.recorder,
		UnitOfWork: f.uow,
		IDs:        &seqIDs{},
		Audit:      app.AuditMeta{Service: "test", ActorType: "staff"},
		Limits: app.CertificationLimits{
			RequireReason:   true,
			AllowBizChannel: true,
		},
	})
	if err != nil {
		t.Fatalf("NewCertificationService: %v", err)
	}

	campaign := createCertCampaign(t, f, []string{role.ID}, false)
	if _, err := svc.ActivateCampaign(context.Background(), campaign.ID, certScope(), "1001"); err != nil {
		t.Fatalf("ActivateCampaign: %v", err)
	}

	batches := repo.batchSizes()
	if len(batches) != 1 {
		t.Fatalf("SaveAll 调用 %d 次（批大小 %v），want 1 次：应用层逐条落库会让 1 万任务变成 1 万次往返", len(batches), batches)
	}
	if batches[0] != len(subjects) {
		t.Fatalf("单批行数=%d, want %d", batches[0], len(subjects))
	}
}
