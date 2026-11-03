// Package domain 定义审计查询与保留的领域契约（ENT-04）。
//
// 本域只负责「读审计」与「到期清理」：写入仍由 internal/shared/audit 的
// Emitter → Outbox → SQLEmitter 链路完成（AUD-03/06），避免出现第二个写入方。
package domain

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// MaxFilterTextLen 是单个过滤条件的最大长度，避免超长串进入 SQL 参数与索引扫描。
const MaxFilterTextLen = 128

// Filter 是审计查询条件。
//
// 时间窗必填且受限：审计表按 occurred_at 索引组织，无界查询会退化为全表扫描。
type Filter struct {
	// From 起始时间（含），必填。
	From time.Time
	// To 结束时间（不含），必填。
	To time.Time
	// ActorID 操作者主体 ID。
	ActorID string
	// Action 稳定动作枚举，如 auth.login。
	Action string
	// Channel 端别：admin / biz / c / worker。
	Channel string
	// Outcome 结果：success / failure / denied。
	Outcome string
	// MerchantID B 端商户 ID。
	MerchantID string
	// ResourceType 目标资源类型。
	ResourceType string
	// ResourceID 目标资源 ID。
	ResourceID string
	// TraceID 请求追踪 ID。
	TraceID string
	// Limit 本次返回条数上限。
	Limit int
	// Offset 偏移量（0-based）。
	Offset int
}

// Validate 校验时间窗、枚举与文本长度。
//
// maxRange 为允许的最大时间跨度，由 audit.query.max_range_days 换算而来。
func (f Filter) Validate(maxRange time.Duration) error {
	if f.From.IsZero() || f.To.IsZero() {
		return fmt.Errorf("from and to are required")
	}
	if !f.To.After(f.From) {
		return fmt.Errorf("to must be after from")
	}
	if maxRange > 0 && f.To.Sub(f.From) > maxRange {
		return fmt.Errorf("time range exceeds maximum of %s", maxRange)
	}
	if f.Limit <= 0 {
		return fmt.Errorf("limit must be positive")
	}
	if f.Offset < 0 {
		return fmt.Errorf("offset must be >= 0")
	}
	switch strings.TrimSpace(f.Outcome) {
	case "", string(audit.OutcomeSuccess), string(audit.OutcomeFailure), string(audit.OutcomeDenied):
	default:
		return fmt.Errorf("unsupported outcome: %s", f.Outcome)
	}
	switch strings.TrimSpace(f.Channel) {
	case "", "admin", "biz", "c", "worker":
	default:
		return fmt.Errorf("unsupported channel: %s", f.Channel)
	}
	for name, value := range map[string]string{
		"actor_id":      f.ActorID,
		"action":        f.Action,
		"merchant_id":   f.MerchantID,
		"resource_type": f.ResourceType,
		"resource_id":   f.ResourceID,
		"trace_id":      f.TraceID,
	} {
		if len(value) > MaxFilterTextLen {
			return fmt.Errorf("%s exceeds %d characters", name, MaxFilterTextLen)
		}
	}
	return nil
}

// Reader 是审计只读仓储。
type Reader interface {
	// List 按条件分页返回事件，按 occurred_at 倒序。
	List(ctx context.Context, filter Filter) ([]audit.Event, error)
	// Count 返回满足条件的总条数。
	Count(ctx context.Context, filter Filter) (int, error)
	// Stream 按条件逐条回调，供导出使用；回调返回错误即中止。
	Stream(ctx context.Context, filter Filter, emit func(audit.Event) error) error
}

// RetentionStore 是保留期清理使用的仓储契约。
type RetentionStore interface {
	// CountBefore 统计 occurred_at 早于 cutoff 的行数。
	CountBefore(ctx context.Context, cutoff time.Time) (int, error)
	// ListBefore 返回 occurred_at 早于 cutoff 的最早 limit 条事件。
	//
	// 归档必须先读到「将要删除的这批行」再落对象存储，随后按 ID 删除；
	// 直接 DeleteBefore 无法保证「先归档后销毁」，归档缺失时就成了静默销毁。
	ListBefore(ctx context.Context, cutoff time.Time, limit int) ([]audit.Event, error)
	// DeleteBefore 分批删除 occurred_at 早于 cutoff 的行，返回本次删除行数。
	//
	// limit <= 0 表示单批上限由实现决定；返回 0 表示已无到期数据。
	DeleteBefore(ctx context.Context, cutoff time.Time, limit int) (int, error)
	// DeleteIDs 按事件 ID 删除，返回删除行数；仅供保留期任务使用。
	DeleteIDs(ctx context.Context, ids []string) (int, error)
}
