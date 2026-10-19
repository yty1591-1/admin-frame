package id

import (
	"fmt"
	"time"
)

// ErrInvalidNode 表示节点号超出允许范围。
type ErrInvalidNode struct {
	NodeID  int64
	MaxNode int64
}

// Error 实现 error。
func (e *ErrInvalidNode) Error() string {
	return fmt.Sprintf("id: node_id %d out of range [0, %d]", e.NodeID, e.MaxNode)
}

// ErrClockRollback 表示检测到时钟回拨且超过容忍阈值。
type ErrClockRollback struct {
	// Backward 回拨时长。
	Backward time.Duration
}

// Error 实现 error。
func (e *ErrClockRollback) Error() string {
	if e == nil {
		return "id: clock moved backwards"
	}
	return fmt.Sprintf("id: clock moved backwards by %s", e.Backward)
}

// ErrOvertime 表示时间戳相对纪元溢出（部署过久或纪元设置过小）。
var ErrOvertime = fmt.Errorf("id: timestamp exceeds bit width")
