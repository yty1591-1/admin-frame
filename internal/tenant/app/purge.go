package app

// ENT-15 数据处置执行器：租户终止后的三段式硬删除（D-05）。
//
// 职责划分（tenant-data-lifecycle.md）：租户域提供「哪些数据、什么顺序、
// 什么边界」（Boundary 端口，runtime 装配租户 / IAM / identity 各自的
// Purger）；本服务提供「怎么删、怎么验」——扫描到期租户 → 校验导出证据 →
// 按边界顺序删除 → tenant.data_purge 审计 → 处置报告。
//
// 铁律（§到期流程）：无导出证据的租户一律跳过并告警，绝不裸删；
// 任一步失败即中止本轮（删除按边界推进，重跑只删剩余行，幂等安全）。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// ActionTenantDataPurge 是处置完成审计动作（EVENTS.md tenant.* 段）。
const ActionTenantDataPurge = "tenant.data_purge"

// Boundary 是一个硬删除边界：一个域的商户数据清理器。
//
// 实现方（runtime 装配）：租户四表 / IAM 商户角色与绑定 / identity 商户账号
// 与会话。Purge 必须幂等——重跑只删剩余行，返回本次删除的行数。
type Boundary interface {
	// Name 边界名（审计 attrs.tables 的组成项，低基数）。
	Name() string
	// Purge 删除该租户在本边界内的数据，返回删除行数。
	Purge(ctx context.Context, tenantID string, batchSize int) (int64, error)
}

// ExportEvidence 是导出证据端口：返回该租户已校验 manifest 的 SHA-256。
// 未登记 manifest 返回 domain.ErrManifestNotFound（处置任务据此跳过）。
type ExportEvidence interface {
	VerifiedManifest(ctx context.Context, tenantID string) (string, error)
}

// PurgeObserver 是处置任务的可观测端口（跳过告警与进度）。
type PurgeObserver interface {
	// TenantSkipped 无导出证据跳过（非零即应关注导出窗口是否被运营遗漏）。
	TenantSkipped(tenantID string)
	// TenantPurged 处置完成。
	TenantPurged(tenantID string, boundaries int, rows int64)
}

// PurgeOptions 是处置任务参数（对应 tenant.retention.* 配置，T-23 约定）。
type PurgeOptions struct {
	// Service 服务名（审计事件来源）。
	Service string
	// HoldDays 保留期天数（默认 90）。
	HoldDays int
	// ExportDays 导出窗口天数（默认 30）。
	ExportDays int
	// BatchSize 分批删除批大小（默认 500）。
	BatchSize int
	// Now 时钟注入（测试）；nil 用 time.Now。
	Now func() time.Time
}

// PurgeReport 是单轮处置报告（供归档与指标）。
type PurgeReport struct {
	// Scanned 本轮扫描到的到期租户数。
	Scanned int
	// Purged 完成处置的租户数。
	Purged int
	// Skipped 因无导出证据跳过的租户数。
	Skipped int
	// RowsDeleted 累计删除行数。
	RowsDeleted int64
	// Entries 每个已处置租户的明细。
	Entries []PurgeEntry
}

// PurgeEntry 是单个租户的处置明细。
type PurgeEntry struct {
	TenantID       string
	RowsDeleted    int64
	Tables         []string
	ManifestSHA256 string
	TerminatedAt   time.Time
}

// purgeDefaults 归一参数默认值。
func (o *PurgeOptions) normalized() (hold, export, batch int) {
	hold, export, batch = o.HoldDays, o.ExportDays, o.BatchSize
	if hold <= 0 {
		hold = 90
	}
	if export <= 0 {
		export = 30
	}
	if batch <= 0 {
		batch = 500
	}
	return
}

// PurgeService 执行三段式处置的第三段（硬删除）。
type PurgeService struct {
	tenants    domain.Repository
	manifests  domain.ExportManifestRepository
	evidence   ExportEvidence
	boundaries []Boundary
	emitter    audit.Emitter
	observer   PurgeObserver
	opts       PurgeOptions
	now        func() time.Time
}

// NewPurgeService 构造处置服务：仓储、manifest 证据、边界与审计出口必填。
func NewPurgeService(tenants domain.Repository, manifests domain.ExportManifestRepository,
	evidence ExportEvidence, boundaries []Boundary, emitter audit.Emitter,
	observer PurgeObserver, opts PurgeOptions) (*PurgeService, error) {
	switch {
	case tenants == nil:
		return nil, apperr.Internal("purge service requires a tenant repository", nil)
	case manifests == nil:
		return nil, apperr.Internal("purge service requires an export manifest repository", nil)
	case evidence == nil:
		return nil, apperr.Internal("purge service requires export evidence", nil)
	case len(boundaries) == 0:
		return nil, apperr.Internal("purge service requires at least one boundary", nil)
	case emitter == nil:
		return nil, apperr.Internal("purge service requires an audit emitter", nil)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	if observer == nil {
		observer = noopPurgeObserver{}
	}
	return &PurgeService{
		tenants:    tenants,
		manifests:  manifests,
		evidence:   evidence,
		boundaries: boundaries,
		emitter:    emitter,
		observer:   observer,
		opts:       opts,
		now:        now,
	}, nil
}

// RunDue 执行一轮到期处置，返回报告；任一租户处置失败即中止本轮
// （已完成的租户保持已删状态，重跑只处理剩余——幂等安全）。
func (s *PurgeService) RunDue(ctx context.Context) (PurgeReport, error) {
	hold, export, batch := s.opts.normalized()
	now := s.now()
	// 截止时点：terminated_at 早于 now - hold - export 才允许进入硬删除。
	before := now.AddDate(0, 0, -(hold + export))

	due, err := s.tenants.ListDueForPurge(ctx, before, 200)
	if err != nil {
		return PurgeReport{}, apperr.Internal("purge scan failed", err)
	}
	report := PurgeReport{Scanned: len(due)}

	for _, tenant := range due {
		entry, perr := s.purgeOne(ctx, tenant, batch)
		if perr != nil {
			// 中止本轮：失败租户保留到下一轮（跳过告警已由 observer 发出）。
			return report, perr
		}
		if entry == nil {
			report.Skipped++
			continue
		}
		report.Purged++
		report.RowsDeleted += entry.RowsDeleted
		report.Entries = append(report.Entries, *entry)
	}
	return report, nil
}

// purgeOne 处置单个租户：证据校验 → 按边界顺序删除 → 审计。
// 返回 nil entry 表示因无导出证据跳过（不视为错误）。
func (s *PurgeService) purgeOne(ctx context.Context, tenant domain.Tenant, batch int) (*PurgeEntry, error) {
	// 第 2 步：确认导出证据存在（无导出 → 跳过并告警，不得直接删）。
	manifest, err := s.manifests.FindLatest(ctx, tenant.ID)
	if err != nil {
		if errors.Is(err, domain.ErrManifestNotFound) {
			s.observer.TenantSkipped(tenant.ID)
			return nil, nil
		}
		return nil, apperr.Internal("load export manifest failed", err)
	}
	// 证据端口复核：SHA 一致性断言（manifest 仓储即默认证据源；端口存在
	// 是为外部证据源留位）。不一致视为证据损坏，按无证据跳过并告警。
	sha, err := s.evidence.VerifiedManifest(ctx, tenant.ID)
	if err != nil {
		if errors.Is(err, domain.ErrManifestNotFound) {
			s.observer.TenantSkipped(tenant.ID)
			return nil, nil
		}
		return nil, apperr.Internal("verify export evidence failed", err)
	}
	if !strings.EqualFold(sha, manifest.ManifestSHA256) {
		s.observer.TenantSkipped(tenant.ID)
		return nil, nil
	}

	// 第 3 步：按边界表顺序分批硬删除。
	entry := &PurgeEntry{
		TenantID:       tenant.ID,
		ManifestSHA256: strings.ToLower(sha),
		Tables:         make([]string, 0, len(s.boundaries)),
	}
	if tenant.TerminatedAt != nil {
		entry.TerminatedAt = *tenant.TerminatedAt
	}
	for _, boundary := range s.boundaries {
		rows, berr := boundary.Purge(ctx, tenant.ID, batch)
		if berr != nil {
			return nil, apperr.Internal(fmt.Sprintf("purge boundary %s failed", boundary.Name()), berr)
		}
		entry.RowsDeleted += rows
		entry.Tables = append(entry.Tables, boundary.Name())
	}

	// 第 4 步：tenant.data_purge 审计（删除之后写入，永久可查）。
	terminatedAt := ""
	if tenant.TerminatedAt != nil {
		terminatedAt = tenant.TerminatedAt.UTC().Format(time.RFC3339)
	}
	// 元审计无业务事件 ID 可借，用纳秒时间戳保证唯一（Validate 必填）。
	eventID := "tenant-purge-" + s.now().UTC().Format("20060102150405.000000000")
	if aerr := s.emitter.Emit(ctx, audit.Event{
		EventID:      eventID,
		Action:       ActionTenantDataPurge,
		ActorID:      "worker",
		ActorType:    "system",
		Channel:      "system",
		ResourceType: "tenant",
		ResourceID:   tenant.ID,
		Outcome:      audit.OutcomeSuccess,
		OccurredAt:   s.now().UTC(),
		Service:      s.opts.Service,
		Attrs: map[string]string{
			"tables":                 strings.Join(entry.Tables, ","),
			"rows_deleted":           fmt.Sprintf("%d", entry.RowsDeleted),
			"export_manifest_sha256": entry.ManifestSHA256,
			"terminated_at":          terminatedAt,
		},
	}); aerr != nil {
		return nil, apperr.Internal("emit tenant.data_purge audit failed", aerr)
	}
	s.observer.TenantPurged(tenant.ID, len(entry.Tables), entry.RowsDeleted)
	return entry, nil
}

// noopPurgeObserver 是观察端口的空实现。
type noopPurgeObserver struct{}

// TenantSkipped 无操作。
func (noopPurgeObserver) TenantSkipped(string) {}

// TenantPurged 无操作。
func (noopPurgeObserver) TenantPurged(string, int, int64) {}

// manifestEvidence 把 ExportManifestRepository 适配为 ExportEvidence
// （默认装配形态：证据即最新 manifest 行）。
type manifestEvidence struct {
	repo domain.ExportManifestRepository
}

// NewManifestEvidence 构造默认证据端口。
func NewManifestEvidence(repo domain.ExportManifestRepository) (ExportEvidence, error) {
	if repo == nil {
		return nil, apperr.Internal("manifest evidence requires a repository", nil)
	}
	return &manifestEvidence{repo: repo}, nil
}

// VerifiedManifest 返回最新 manifest 的 SHA-256。
func (e *manifestEvidence) VerifiedManifest(ctx context.Context, tenantID string) (string, error) {
	m, err := e.repo.FindLatest(ctx, tenantID)
	if err != nil {
		return "", err
	}
	return m.ManifestSHA256, nil
}
