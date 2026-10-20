package cache

import "time"

// Clock 可注入时钟，便于单测过期与抖动。
type Clock interface {
	Now() time.Time
}

type realClock struct{}

// Now 返回墙钟当前时间。
func (realClock) Now() time.Time { return time.Now() }

// RealClock 返回墙钟。
func RealClock() Clock { return realClock{} }
