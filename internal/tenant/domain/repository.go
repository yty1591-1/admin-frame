package domain

import (
	"context"
	"strings"
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
	// UpdateStatus 以 CAS 方式写入迁移结果。
	//
	// 约定：tenant.Version 是**迁移后**的新版本，仓储以其减一作为预期旧版本；
	// 版本不匹配返回 ErrVersionConflict，保证并发「停用 + 启用」不会互相覆盖。
	UpdateStatus(ctx context.Context, tenant Tenant) error
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
	// ListMembers 列出租户成员；includeDisabled=false 时只返回在册成员。
	ListMembers(ctx context.Context, tenantID string, includeDisabled bool) ([]Member, error)
	// CountActiveByRole 统计某角色在册成员数（用于「最后一名 owner」校验）。
	CountActiveByRole(ctx context.Context, tenantID string, role MemberRole) (int, error)
}

// QuotaRepository 是租户配额持久化契约。
type QuotaRepository interface {
	// ListQuotas 列出租户全部配额；未配置的资源不出现在结果里。
	ListQuotas(ctx context.Context, tenantID string) ([]Quota, error)
	// UpsertQuota 按 (tenant_id, resource) 幂等写入上限。
	UpsertQuota(ctx context.Context, quota Quota) error
}
