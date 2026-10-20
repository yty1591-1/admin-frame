package migrate

import (
	"fmt"
	"io"
	"time"
)

// EventKind 表示迁移生命周期事件类型。
type EventKind string

const (
	// EventPlan 表示 dry-run 计划执行某条迁移（未执行 SQL）。
	EventPlan EventKind = "plan"
	// EventStart 表示即将执行一条迁移。
	EventStart EventKind = "start"
	// EventDone 表示一条迁移执行成功。
	EventDone EventKind = "done"
	// EventError 表示一条迁移执行失败。
	EventError EventKind = "error"
)

// Event 描述一条迁移的执行状态；由 Runner 同步回调给 Observer。
//
// 回调不得阻塞或 panic；不得在回调中调用同一个 Runner 的 Up/Down（会自锁）。
type Event struct {
	Kind        EventKind
	Direction   Direction
	Version     int64
	Description string
	Statements  int
	Duration    time.Duration
	Err         error
}

// Observer 接收迁移事件；实现须并发安全（当前实现按顺序回调，但未来可能并行）。
type Observer func(Event)

// TextReporter 返回将事件以人类可读格式写入 w 的 Observer（供 cmd/migrate 使用）。
//
// w 为 nil 时返回 nil，调用方可直接赋给 Config.Observer。
func TextReporter(w io.Writer) Observer {
	if w == nil {
		return nil
	}
	return func(e Event) {
		name := fmt.Sprintf("%06d_%s", e.Version, e.Description)
		switch e.Kind {
		case EventPlan:
			reportf(w, "==> would %s %s (%d statements)\n", verb(e.Direction), name, e.Statements)
		case EventStart:
			reportf(w, "==> %s %s (%d statements)\n", verb(e.Direction), name, e.Statements)
		case EventDone:
			reportf(w, "    OK %s (%s)\n", name, e.Duration.Round(time.Millisecond))
		case EventError:
			reportf(w, "    FAIL %s: %v\n", name, e.Err)
		}
	}
}

// reportf 写 CLI 输出；写入失败无可恢复动作（终端断开时进程通常已结束），故忽略。
func reportf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func verb(d Direction) string {
	if d == DirectionDown {
		return "reverting"
	}
	return "applying"
}
