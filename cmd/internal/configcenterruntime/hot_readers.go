package configcenterruntime

import (
	"time"
)

// HotDuration 返回「每次调用都读当前热更视图」的时长取值函数。
//
// 视图缺该键时回落到 fallback（通常取配置文件静态值），因此首次发布前
// 与改造前行为一致；视图存在时以热更值优先（ADR-0015）。
//
// 调用方应把返回的函数挂在中间件 / 循环上按次调用，而不是在装配期取一次值，
// 否则热更不会生效。
func (r *Runtime) HotDuration(key string, fallback time.Duration) func() time.Duration {
	if r == nil || r.Hot == nil {
		return func() time.Duration { return fallback }
	}
	return func() time.Duration {
		return r.Hot.Current().Duration(key, fallback)
	}
}

// HotInt 返回读取当前热更视图整数键的函数；缺键回落 fallback。
func (r *Runtime) HotInt(key string, fallback int64) func() int64 {
	if r == nil || r.Hot == nil {
		return func() int64 { return fallback }
	}
	return func() int64 {
		return r.Hot.Current().Int(key, fallback)
	}
}

// HotBool 返回读取当前热更视图布尔键的函数；缺键回落 fallback。
func (r *Runtime) HotBool(key string, fallback bool) func() bool {
	if r == nil || r.Hot == nil {
		return func() bool { return fallback }
	}
	return func() bool {
		return r.Hot.Current().Bool(key, fallback)
	}
}

// HotString 返回读取当前热更视图字符串键的函数；缺键回落 fallback。
func (r *Runtime) HotString(key, fallback string) func() string {
	if r == nil || r.Hot == nil {
		return func() string { return fallback }
	}
	return func() string {
		return r.Hot.Current().String(key, fallback)
	}
}
