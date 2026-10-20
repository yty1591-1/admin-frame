package circuitbreaker

// State 是熔断器状态。
type State int

const (
	// StateClosed 表示闭合：请求正常放行，并累计成功/失败用于判定。
	StateClosed State = iota
	// StateOpen 表示打开：请求立即失败，等待 Timeout 后进入半开探测。
	StateOpen
	// StateHalfOpen 表示半开：只放行 MaxRequests 个探测请求，决定恢复或再次打开。
	StateHalfOpen
)

// String 返回稳定的状态名，便于日志与指标标签。
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}
