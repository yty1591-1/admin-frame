package circuitbreaker

import (
	"sync"
	"time"
)

type transition struct {
	from State
	to   State
}

// Breaker 是并发安全的进程内熔断器；创建后不可复制。
//
// 状态机：
//   - Closed：放行请求；ReadyToTrip 返回 true 时转 Open
//   - Open：立即返回 ErrOpen；等待 Timeout 后转 HalfOpen
//   - HalfOpen：最多放行 MaxRequests 个并发探测；全部成功转 Closed，任一失败转 Open
type Breaker struct {
	cfg Config

	mu         sync.Mutex
	state      State
	generation uint64
	counts     Counts
	expiry     time.Time
}

// New 创建熔断器；负 Interval/Timeout 返回 ErrInvalidConfig，其余零值按默认值归一。
func New(cfg Config) (*Breaker, error) {
	normalized, err := cfg.normalize()
	if err != nil {
		return nil, err
	}
	b := &Breaker{cfg: normalized, state: StateClosed}
	if normalized.Interval > 0 {
		b.expiry = normalized.Clock.Now().Add(normalized.Interval)
	}
	return b, nil
}

// MustNew 同 New，配置非法时 panic；仅用于启动期常量配置。
func MustNew(cfg Config) *Breaker {
	b, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return b
}

// Name 返回配置的实例名。
func (b *Breaker) Name() string { return b.cfg.Name }

// State 返回当前状态；可能顺带完成 Open→HalfOpen 或统计窗口重置。
func (b *Breaker) State() State {
	if b == nil {
		return StateClosed
	}
	b.mu.Lock()
	state, _, tr := b.currentStateLocked(b.cfg.Clock.Now())
	b.mu.Unlock()
	b.notify(tr)
	return state
}

// Counts 返回当前统计窗口的计数快照；可能顺带完成状态迁移。
func (b *Breaker) Counts() Counts {
	if b == nil {
		return Counts{}
	}
	b.mu.Lock()
	_, _, tr := b.currentStateLocked(b.cfg.Clock.Now())
	counts := b.counts
	b.mu.Unlock()
	b.notify(tr)
	return counts
}

// Do 通过熔断器执行 fn。
//
// 返回：
//   - fn 的错误原样返回（成功与否由 Config.IsSuccessful 判定）
//   - ErrOpen / ErrTooManyRequests：请求未执行
//   - ErrNilFunc：fn 为 nil
//   - ErrNilBreaker：在 nil 接收者上调用
//
// fn panic 时先计为失败，再把 panic 继续向上抛出。
func (b *Breaker) Do(fn func() error) error {
	if b == nil {
		return ErrNilBreaker
	}
	if fn == nil {
		return ErrNilFunc
	}
	before, err := b.beforeRequest()
	if err != nil {
		return err
	}
	success := false
	defer func() {
		if r := recover(); r != nil {
			b.afterRequest(before, false)
			panic(r)
		}
		b.afterRequest(before, success)
	}()
	err = fn()
	success = b.cfg.IsSuccessful(err)
	return err
}

// DoValue 与 Breaker.Do 相同，但保留成功时的返回值。
//
// 由于 Go 不允许方法带类型参数，本函数为包级函数；b 必须非 nil。
func DoValue[T any](b *Breaker, fn func() (T, error)) (T, error) {
	var zero T
	if b == nil {
		return zero, ErrNilBreaker
	}
	if fn == nil {
		return zero, ErrNilFunc
	}
	before, err := b.beforeRequest()
	if err != nil {
		return zero, err
	}
	success := false
	defer func() {
		if r := recover(); r != nil {
			b.afterRequest(before, false)
			panic(r)
		}
		b.afterRequest(before, success)
	}()
	v, err := fn()
	success = b.cfg.IsSuccessful(err)
	return v, err
}

// Reset 强制回到 Closed 并清空统计；用于运维/测试，生产调用应受权限控制。
func (b *Breaker) Reset() {
	if b == nil {
		return
	}
	b.mu.Lock()
	tr := b.setStateLocked(StateClosed, b.cfg.Clock.Now())
	b.mu.Unlock()
	b.notify(tr)
}

func (b *Breaker) beforeRequest() (uint64, error) {
	b.mu.Lock()
	state, generation, tr := b.currentStateLocked(b.cfg.Clock.Now())
	if state == StateOpen {
		b.mu.Unlock()
		b.notify(tr)
		return generation, ErrOpen
	}
	if state == StateHalfOpen && b.counts.Requests >= b.cfg.MaxRequests {
		b.mu.Unlock()
		b.notify(tr)
		return generation, ErrTooManyRequests
	}
	b.counts.onRequest()
	b.mu.Unlock()
	b.notify(tr)
	return generation, nil
}

func (b *Breaker) afterRequest(before uint64, success bool) {
	b.mu.Lock()
	now := b.cfg.Clock.Now()
	state, generation, timed := b.currentStateLocked(now)
	var changed *transition
	if generation == before {
		if success {
			changed = b.onSuccessLocked(state, now)
		} else {
			changed = b.onFailureLocked(state, now)
		}
	}
	b.mu.Unlock()
	b.notify(timed)
	b.notify(changed)
}

func (b *Breaker) onSuccessLocked(state State, now time.Time) *transition {
	switch state {
	case StateClosed:
		b.counts.onSuccess()
	case StateHalfOpen:
		b.counts.onSuccess()
		if b.counts.ConsecutiveSuccesses >= b.cfg.MaxRequests {
			return b.setStateLocked(StateClosed, now)
		}
	default:
		// Open 状态的过期结果已被 generation 过滤；无需处理。
	}
	return nil
}

func (b *Breaker) onFailureLocked(state State, now time.Time) *transition {
	switch state {
	case StateClosed:
		b.counts.onFailure()
		if b.cfg.ReadyToTrip(b.counts) {
			return b.setStateLocked(StateOpen, now)
		}
	case StateHalfOpen:
		b.counts.onFailure()
		return b.setStateLocked(StateOpen, now)
	default:
		// Open 状态的过期结果已被 generation 过滤；无需处理。
	}
	return nil
}

// currentStateLocked 应用时间驱动的状态迁移，并返回迁移后的状态与代号。
//
// 调用方必须持有 b.mu；返回的 transition 应在解锁后通过 notify 派发。
func (b *Breaker) currentStateLocked(now time.Time) (State, uint64, *transition) {
	if b.state == StateClosed && b.cfg.Interval > 0 && !now.Before(b.expiry) {
		b.generation++
		b.counts = Counts{}
		b.expiry = now.Add(b.cfg.Interval)
	}
	if b.state == StateOpen && !now.Before(b.expiry) {
		tr := b.setStateLocked(StateHalfOpen, now)
		return b.state, b.generation, tr
	}
	return b.state, b.generation, nil
}

// setStateLocked 迁移状态并重置统计；调用方必须持有 b.mu。
func (b *Breaker) setStateLocked(state State, now time.Time) *transition {
	prev := b.state
	b.state = state
	b.generation++
	b.counts = Counts{}
	switch state {
	case StateOpen:
		b.expiry = now.Add(b.cfg.Timeout)
	case StateClosed:
		if b.cfg.Interval > 0 {
			b.expiry = now.Add(b.cfg.Interval)
		} else {
			b.expiry = time.Time{}
		}
	case StateHalfOpen:
		b.expiry = time.Time{}
	}
	if prev == state {
		return nil
	}
	return &transition{from: prev, to: state}
}

func (b *Breaker) notify(tr *transition) {
	if tr == nil || b.cfg.OnStateChange == nil {
		return
	}
	b.cfg.OnStateChange(b.cfg.Name, tr.from, tr.to)
}
