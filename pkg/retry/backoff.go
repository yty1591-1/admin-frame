package retry

import (
	"math"
	"math/rand"
	"time"
)

// backoffDelay 计算第 attempt 次失败后的休眠时长（attempt 从 1 起）。
// 先算指数基准，再按 JitterFraction 抖动，并夹紧到 MaxBackoff。
func (p Policy) backoffDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	// initial * multiplier^(attempt-1)
	base := float64(p.InitialBackoff) * math.Pow(p.Multiplier, float64(attempt-1))
	if base > float64(p.MaxBackoff) {
		base = float64(p.MaxBackoff)
	}
	if base < 0 {
		base = 0
	}

	delay := base
	if p.JitterFraction > 0 && base > 0 {
		// full jitter 的比例形式：delay = base * (1 - J) + random(0, base * J * 2) 在 J=1 时等价 random(0, base)
		// 采用：delay = random(base*(1-J), base] 当 J∈(0,1]；J=1 → [0, base]
		lower := base * (1 - p.JitterFraction)
		span := base - lower
		delay = lower + rand.Float64()*span //nolint:gosec // 退避抖动无需密码学随机
	}
	d := time.Duration(delay)
	if d < 0 {
		return 0
	}
	if d > p.MaxBackoff {
		return p.MaxBackoff
	}
	return d
}
