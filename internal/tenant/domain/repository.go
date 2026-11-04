package domain

import (
	"context"
	"strings"
	"time"
)

// Filter 是租户列表的过滤条件。
//
// Keyword 只匹配名称前缀 / 包含，不做跨字段模糊搜索：租户表是隔离边界，
// 宽泛匹配会让运营误操作到别的租户。
type Filter struct {
	// Status 状态过滤；空表示全部。
	Status Status
	// Keyword 名称关键字；空表示不过滤。
	Keyword string
	// Limit 每页条数；<=0 时由应用层补默认值。
	Limit int
	// Offset 偏移量。
	Offset int
}

// Normalize 归一化过滤条件：去空白关键字、补齐分页默认值。
func (f Filter) Normalize() Filter {
	out := f
	out.Keyword = strings.TrimSpace(f.Keyword)
	if out.Limit <= 0 {
		out.Limit = 20
	}
	if out.Offset < 0 {
		out.Offset = 0
	}
	return out
}

// Repository 是租户持久化契约。
//
// 只暴露生命周期所需的四个动作：状态迁移不提供「任意字段更新」，
// 避免名称 / 状态被绕过状态机直接改写。
type Repository interface {
	// Create 新建租户；主键冲突返回错误（调用方应已用 ID 生成器避免）。
	Create(ctx context.Context, tenant Tenant) error
	// Get 按 ID 读取；不存在返回 ErrNotFound。
	Get(ctx context.Context, id string) (Tenant, error)
	// List 分页查询，返回当页数据与总数。
	List(ctx context.Context, filter Filter) ([]Tenant, int, error)
	// WithinTx 在事务内执行 fn，并把事务注入 ctx。
	//
	// 用途：把业务写入与审计 Outbox 记录放进同一事务，避免「状态已改、审计丢失」
	// 或「审计已写、状态回滚」。fn 内必须通过同一个 ctx 调用仓储与 Emitter。
	// 内存实现不提供事务语义，仅顺序执行（本地开发不追求原子性）。
	WithinTx(ctx context.Context, fn func(context.Context) error) error
	// UpdateStatus 以 CAS 方式写入迁移结果。
	//
	// 约定：tenant.Version 是**迁移后**的新版本，仓储以其减一作为预期旧版本；
	// 版本不匹配返回 ErrVersionConflict，保证并发「停用 + 启用」不会互相覆盖。
	UpdateStatus(ctx context.Context, tenant Tenant) error
	// UpdateProfile 以 CAS 方式写入资料变更（T-11：改名 + 元数据）。
	//
	// 与 UpdateStatus 共用同一个版本计数器：并发「改名 + 停用」互为冲突，
	// 后写者拿到 409 重读重试，而不是悄悄覆盖对方的写入。
	UpdateProfile(ctx context.Context, tenant Tenant) error
	// CountByStatus 按状态统计租户数（T-16 的 tenant_status Gauge 数据源）。
	// 返回的 map 只包含有租户的状态；查询失败返回 error，由调用方决定降级方式。
	CountByStatus(ctx context.Context) (map[Status]int, error)

	// ListDueForPurge 返回保留期 + 导出窗口均已届满的 terminated 租户
	// （ENT-15 D-05 三段式处置的扫描口径：terminated_at < before）。
	// 按 terminated_at 升序返回（先到期的先处置），limit 上限保护单轮批量。
	ListDueForPurge(ctx context.Context, before time.Time, limit int) ([]Tenant, error)
}

// MemberRepository 是租户成员持久化契约。
type MemberRepository interface {
	// Mutate 在「同一租户串行」的临界区内执行 fn。
	//
	// 存在的理由是一个跨行不变量：租户必须至少保留一名在册 owner。
	// 计数与写入若不在同一临界区，两个 owner 并发降级会都读到 count=2 而全部通过，
	// 最终留下 0 名 owner。SQL 实现用事务 + 租户行 FOR UPDATE，
	// 内存实现用独立互斥锁；fn 内必须通过同一个 ctx 调用本仓储的方法以复用事务。
	Mutate(ctx context.Context, tenantID string, fn func(context.Context) error) error
	// UpsertMember 按 (tenant_id, subject_id) 幂等写入：不存在则插入，存在则覆盖角色与状态。
	UpsertMember(ctx context.Context, member Member) error
	// GetMember 读取单条成员；不存在返回 ErrMemberNotFound。
	GetMember(ctx context.Context, tenantID, subjectID string) (Member, error)
	// ListMembers 分页列出租户成员（T-13）。
	//
	// includeDisabled=false 时只返回在册成员；limit<=0 表示不分页（全量），
	// 供席位计数等内部路径复用；返回值第二项是满足条件的总数。
	ListMembers(ctx context.Context, tenantID string, includeDisabled bool, limit, offset int) ([]Member, int, error)
	// CountActiveByRole 统计某角色在册成员数（用于「最后一名 owner」校验）。
	CountActiveByRole(ctx context.Context, tenantID string, role MemberRole) (int, error)
	// CountActive 统计在册成员总数（T-13 席位上限的当前值）。
	//
	// 事务内调用必须看到未提交写入之外的一致快照：SQL 实现追加 FOR UPDATE，
	// 与「锁租户行」配合保证并发添加不会同时通过席位校验。
	CountActive(ctx context.Context, tenantID string) (int, error)
}

// QuotaRepository 是租户配额持久化契约。
type QuotaRepository interface {
	// ListQuotas 列出租户全部配额；未配置的资源不出现在结果里。
	ListQuotas(ctx context.Context, tenantID string) ([]Quota, error)
	// UpsertQuota 按 (tenant_id, resource) 幂等写入上限。
	UpsertQuota(ctx context.Context, quota Quota) error
}

// PlanRepository 是套餐目录的持久化契约。
//
// 目录由迁移种子维护（000012），本切片不提供增删改接口——
// 运营改套餐走迁移或后续管理端，避免「改了目录、存量租户怎么办」
// 的语义问题在没有任何工具的情况下被随手触发。
type PlanRepository interface {
	// DefaultPlan 返回默认套餐；不存在返回 ErrNoDefaultPlan。
	DefaultPlan(ctx context.Context) (Plan, error)
	// FindByCode 按编码加载套餐；不存在返回 ErrPlanNotFound。
	FindByCode(ctx context.Context, code string) (Plan, error)
}

// InvitationRepository 是成员邀请的持久化契约（T-14）。
type InvitationRepository interface {
	// CreateInvitation 插入邀请；同租户同邮箱的 pending 唯一冲突返回
	// ErrInvitationPendingExists（数据库生成列唯一索引兜底并发）。
	CreateInvitation(ctx context.Context, invitation Invitation) error
	// GetInvitation 按 (tenant, invitation) 读取；不存在返回 ErrInvitationNotFound。
	GetInvitation(ctx context.Context, tenantID, invitationID string) (Invitation, error)
	// FindByTokenHash 按摘要查找邀请（接受入口）；不存在返回 ErrInvitationNotFound。
	FindByTokenHash(ctx context.Context, tokenHash string) (Invitation, error)
	// ListInvitations 分页列出租户邀请；status 为空表示全部。
	ListInvitations(ctx context.Context, tenantID, status string, limit, offset int) ([]Invitation, int, error)
	// CompleteInvitation 以 `WHERE id AND status='pending'` 的 CAS 写入终态
	// （accepted / revoked / expired）；0 行说明邀请已被并发处理，调用方
	// 应重读并按幂等语义处理（T-14 验收：重复接受幂等）。
	CompleteInvitation(ctx context.Context, invitation Invitation) error
	// RefreshInvitation 重发：覆盖摘要、顺延过期时间，仅 pending 可刷新
	// （0 行返回 ErrInvitationNotPending）。
	RefreshInvitation(ctx context.Context, invitation Invitation) error
}
