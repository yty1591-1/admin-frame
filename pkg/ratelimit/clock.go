package ratelimit

import "time"

// Clock 可注入时钟，便于单测确定性推进时间。
type Clock interface {
	Now() time.Time
}

type realClock struct{}

// Now 返回墙钟时间。
func (realClock) Now() time.Time { return time.Now() }

// RealClock 返回使用 time.Now 的时钟。
func RealClock() Clock { return realClock{} }

// ManualClock 可供测试手动拨快的时钟（零值可用，从 Unix 0 起）。
type ManualClock struct {
	t time.Time
}

// NewManualClock 从 t 创建手动时钟；t 为零值时使用 Unix epoch。
func NewManualClock(t time.Time) *ManualClock {
	if t.IsZero() {
		t = time.Unix(0, 0).UTC()
	}
	return &ManualClock{t: t}
}

// Now 返回当前模拟时间。
func (c *ManualClock) Now() time.Time {
	if c == nil {
		return time.Unix(0, 0).UTC()
	}
	return c.t
}

// Advance 将时钟前移 d。
func (c *ManualClock) Advance(d time.Duration) {
	if c == nil {
		return
	}
	c.t = c.t.Add(d)
}

// Set 将时钟设为绝对时间 t。
func (c *ManualClock) Set(t time.Time) {
	if c == nil {
		return
	}
	c.t = t
}
