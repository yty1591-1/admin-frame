package outbox

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrInvalidEvent 表示事件字段不合法。
	ErrInvalidEvent = errors.New("outbox: invalid event")
	// ErrInvalidState 表示事件状态不允许当前迁移。
	ErrInvalidState = errors.New("outbox: invalid state")
	// ErrNotClaimed 表示事件已被其它 Relay 认领。
	ErrNotClaimed = errors.New("outbox: event is not claimed")
)

// Stats 是 Outbox 积压统计快照。
// ReplayRecentWindow 是 Stats.ReplayedRecently 的统计窗口。
//
// 它与 async.dlq.replay_ineffective 告警的语义绑定：改这个值等于改告警里「近期」的口径，
// 须同步 docs/runbooks/async-dlq.md 与 ALERTS.md 的描述。
const ReplayRecentWindow = 30 * time.Minute

// Stats 是 Outbox 与死信的积压快照。
type Stats struct {
	// Pending 未完成事件数（含 pending / processing / dead / failed）。
	Pending int
	// DLQ 未重放的死信数。
	DLQ int
	// OldestCreatedAt 最老未完成事件的入盒时间；零值表示无积压。
	OldestCreatedAt time.Time
	// ReplayedRecently 最近 ReplayRecentWindow 内被重放的死信行数。
	//
	// 只表达「近期确实有人重放过」，不代表重放成功；与 DLQ > 0 同时成立即
	// 「重放已发起但未清空」，用于把「没人重放」与「重放无效」分开归因。
	ReplayedRecently int
}

// Store 是事务性 Outbox 的持久化抽象。
//
// Append 必须与业务写入处于同一事务（由调用方通过 UnitOfWork 传入事务上下文）；
// 认领、回收与终态迁移使用条件更新，保证多副本只有一个 Relay 投递同一事件。
type Store interface {
	// Append 写入一条 pending 事件；主键冲突返回错误。
	Append(ctx context.Context, event *Event) error
	// ClaimBatch 原子认领到期事件并设置租约；返回的事件状态为 processing。
	ClaimBatch(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]*Event, error)
	// MarkSucceeded 标记投递成功。
	MarkSucceeded(ctx context.Context, eventID string, now time.Time) error
	// Reschedule 记录一次可重试失败并放回 pending。
	Reschedule(ctx context.Context, eventID string, attempts int, nextRetryAt, now time.Time, lastError string) error
	// MarkDead 标记重试耗尽，并写入死信表。
	MarkDead(ctx context.Context, eventID string, now time.Time, lastError string) error
	// MarkFailed 标记永久失败（毒消息），并写入死信表。
	MarkFailed(ctx context.Context, eventID string, now time.Time, lastError string) error
	// ReclaimStale 把租约过期的 processing 事件回收为 pending，返回回收条数。
	ReclaimStale(ctx context.Context, staleBefore, now time.Time) (int, error)
	// Stats 返回积压与死信统计。
	Stats(ctx context.Context) (Stats, error)
}

// DeadLetterReplayer 重放死信：把对应事件重置为 pending 并标记 async_dlq.replayed_at。
//
// 用于修复 handler 后人工恢复；重放后事件重新进入 Relay 投递流程。
type DeadLetterReplayer interface {
	// ReplayDeadLetter 重放单条死信；返回 false 表示该死信不存在或已重放。
	ReplayDeadLetter(ctx context.Context, sourceID string, now time.Time) (bool, error)
	// ReplayAllDeadLetters 重放全部未重放死信，返回重放条数。
	ReplayAllDeadLetters(ctx context.Context, now time.Time) (int, error)
}

// TopicStatReader 按主题返回积压统计，供业务域保留自己的 pending / lag 指标。
type TopicStatReader interface {
	// TopicStats 返回指定主题的积压统计。
	TopicStats(ctx context.Context, topic string) (Stats, error)
}
