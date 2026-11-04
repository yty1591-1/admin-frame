package domain

import (
	"context"
	"strings"
)

// RequestFilter 是通知单列表的过滤条件。
//
// TenantID 必填（隔离边界约束 5.1.1：过滤在仓储层强制，不靠调用方自觉）；
// 平台级运维查询显式传 PlatformTenantID。
type RequestFilter struct {
	// TenantID 租户隔离键；必填。
	TenantID string
	// Scene 场景过滤；空表示全部。
	Scene string
	// Status 状态过滤；空表示全部。
	Status RequestStatus
	// Limit 每页条数；<=0 时取默认值。
	Limit int
	// Offset 偏移量。
	Offset int
}

// Normalize 归一化过滤条件：补齐分页默认值。
func (f RequestFilter) Normalize() RequestFilter {
	out := f
	out.Scene = strings.TrimSpace(f.Scene)
	if out.Limit <= 0 {
		out.Limit = 20
	}
	if out.Offset < 0 {
		out.Offset = 0
	}
	return out
}

// RequestRepository 是通知单持久化契约。
type RequestRepository interface {
	// Create 新建通知单；幂等键冲突返回 ErrDuplicateRequest（消费端
	// 据此把重复事件当作「已受理」幂等吸收，而不是报错触发重试风暴）。
	Create(ctx context.Context, n Notification) error
	// Get 按 ID 读取；不存在返回 ErrNotFound。
	Get(ctx context.Context, id string) (Notification, error)
	// FindByIdempotencyKey 按幂等键查找；不存在返回 ErrNotFound。
	// 消费入口的快速去重路径：先查键再落库，命中即跳过整条编排。
	FindByIdempotencyKey(ctx context.Context, key string) (Notification, error)
	// UpdateStatus 以 CAS 方式写入状态迁移结果。
	//
	// 约定与租户域一致：n.Version 是迁移后的新版本，仓储以其减一作为
	// 预期旧版本；版本不匹配返回 ErrVersionConflict。回执回写与编排
	// 进度并发更新靠这里收敛，后到者拿到冲突后重读重推。
	UpdateStatus(ctx context.Context, n Notification) error
	// List 分页查询，返回当页数据与总数。
	List(ctx context.Context, filter RequestFilter) ([]Notification, int, error)
	// WithinTx 在事务内执行 fn 并把事务注入 ctx。
	//
	// 用途：通知单与渠道明细同事务落库，避免「请求已建、明细丢失」
	// 的中间态。内存实现仅顺序执行（本地开发不追求原子性）。
	WithinTx(ctx context.Context, fn func(context.Context) error) error
}

// DeliveryRepository 是渠道投递明细的持久化契约。
type DeliveryRepository interface {
	// Upsert 按 (request_id, channel) 幂等写入：不存在则插入，
	// 存在则覆盖可变字段（编排重试路径复用同一行，attempt 累加由调用方控制）。
	Upsert(ctx context.Context, d Delivery) error
	// Get 按 (request_id, channel) 读取；不存在返回 ErrDeliveryNotFound。
	Get(ctx context.Context, requestID string, channel Channel) (Delivery, error)
	// ListByRequest 列出通知单的全部渠道明细。
	ListByRequest(ctx context.Context, requestID string) ([]Delivery, error)
	// Update 以 CAS 方式写入明细状态迁移结果（含 attempt 与诊断字段）。
	Update(ctx context.Context, d Delivery) error
	// CountByStatus 按状态统计明细数（notify_delivery Gauge 数据源）。
	CountByStatus(ctx context.Context) (map[DeliveryStatus]int, error)
}

// TemplateRepository 是模板的持久化契约。
type TemplateRepository interface {
	// Upsert 写入模板：同 (key, channel, locale) 已存在时版本 +1 覆盖
	// 为新版本（版本语义：内容变更即新版本，渲染按版本锁定）。
	Upsert(ctx context.Context, t Template) (Template, error)
	// Find 定位启用中的最新版本模板；不存在或已停用返回 ErrTemplateNotFound。
	Find(ctx context.Context, key string, channel Channel, locale string) (Template, error)
	// List 按模板键列出全部版本（管理端查询）。
	List(ctx context.Context, key string) ([]Template, error)
}

// PreferenceRepository 是渠道偏好的持久化契约。
type PreferenceRepository interface {
	// Upsert 按 (tenant_id, subject_type, subject_id, scene_class, channel)
	// 幂等写入偏好。
	Upsert(ctx context.Context, p Preference) error
	// Get 读取单条偏好；不存在返回 ErrNotFound（语义：默认允许）。
	Get(ctx context.Context, tenantID string, subjectType SubjectType,
		subjectID string, class SceneClass, channel Channel) (Preference, error)
	// ListForSubject 列出主体的全部偏好（管理端 / 退订页查询）。
	ListForSubject(ctx context.Context, tenantID string, subjectType SubjectType, subjectID string) ([]Preference, error)
}

// BlacklistRepository 是渠道黑名单的持久化契约。
type BlacklistRepository interface {
	// Add 加入黑名单；(tenant_id, channel, target_hmac) 已存在时幂等返回。
	Add(ctx context.Context, b Blacklist) error
	// Exists 判断目标是否在黑名单（租户级或平台级命中即 true）。
	Exists(ctx context.Context, tenantID string, channel Channel, targetHMAC string) (bool, error)
	// Remove 移除黑名单条目；不存在返回 ErrNotFound。
	Remove(ctx context.Context, tenantID string, channel Channel, targetHMAC string) error
	// List 列出租户黑名单（含平台级条目）。
	List(ctx context.Context, tenantID string) ([]Blacklist, error)
}
