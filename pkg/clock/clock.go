package clock

import "time"

// Clock 提供当前时间；实现必须并发安全。
type Clock interface {
	// Now 返回当前时间。返回值保留时区信息，调用方按需用 In 转换。
	Now() time.Time
}

// Func 将函数适配为 Clock，便于在测试或既有代码中桥接自定义时间源。
//
// 零值（nil 函数）调用 Now 返回零时间，不 panic；生产路径应显式提供函数。
type Func func() time.Time

// Now 调用底层函数；f 为 nil 时返回零时间。
func (f Func) Now() time.Time {
	if f == nil {
		return time.Time{}
	}
	return f()
}

type realClock struct{}

// Now 返回墙钟时间。
func (realClock) Now() time.Time { return time.Now() }

// RealClock 返回基于 time.Now 的时钟；可安全并发使用。
func RealClock() Clock { return realClock{} }

// 编译期断言。
var _ Clock = Func(nil)
var _ Clock = realClock{}
