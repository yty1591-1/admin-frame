package domain

import (
	"context"
	"time"
)

// EvalContext 是评估上下文：判定某一主体在某渠道 / 租户下是否命中 flag。
type EvalContext struct {
	// Channel 端别（admin / biz / c）。
	Channel string
	// TenantID 生效租户（B 端为 CurrentTenant；平台级为空）。
	TenantID string
	// SubjectID 主体标识；百分比灰度的分桶键。空主体对 percentage
	// 规则恒不命中（fail closed——评估必须可复现，不落随机数）。
	SubjectID string
}

// Evaluator 是评估端口：消费方（业务域）定义自己的窄接口，
// runtime 装配本包实现注入；未装配时注入 noop（全走 default_enabled，
// 最小部署形态可运行）。
type Evaluator interface {
	// IsEnabled 返回 flag 在给定上下文下的生效值。
	//
	// 评估顺序（feature-flag-plan.md §0.3）：
	//  kill switch（enabled=false 无条件 false）→ 规则按序（首个命中）→
	//  default_enabled 回退。未知 key 返回 false（fail closed）。
	IsEnabled(ctx context.Context, key string, evalCtx EvalContext) bool
}

// Repository 是 flag 持久化契约。
type Repository interface {
	// Create 写入新 flag；key 冲突返回 ErrDuplicateKey。
	Create(ctx context.Context, f Flag) error
	// Get 按 key 读取；不存在返回 ErrNotFound。
	Get(ctx context.Context, key string) (Flag, error)
	// List 按 state 过滤列出（state 空 = 全部；expired/disabled/enabled）。
	List(ctx context.Context, state string, limit, offset int) ([]Flag, int, error)
	// Update 以 CAS 更新（expectedVersion 不匹配返回 ErrVersionConflict）。
	Update(ctx context.Context, f Flag, expectedVersion int64) error
	// Delete 物理删除；仅允许对非 enabled 状态执行（守卫在 app 层，
	// 仓储只存）。
	Delete(ctx context.Context, key string) error
	// ListExpired 列出到期时刻仍为 enabled 的 flag（到期任务数据源）。
	ListExpired(ctx context.Context, now time.Time, limit int) ([]Flag, error)
}
