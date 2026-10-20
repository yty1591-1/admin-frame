package circuitbreaker

import (
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
)

const (
	defaultMaxRequests = 1
	defaultTimeout     = 60 * time.Second
	defaultName        = "default"
	defaultFailures    = 5
)

// Counts 是当前统计窗口内的请求计数快照。
type Counts struct {
	// Requests 是已放行并正在统计的请求总数。
	Requests uint32
	// TotalSuccesses 是窗口内成功总数。
	TotalSuccesses uint32
	// TotalFailures 是窗口内失败总数。
	TotalFailures uint32
	// ConsecutiveSuccesses 是连续成功数；失败时清零。
	ConsecutiveSuccesses uint32
	// ConsecutiveFailures 是连续失败数；成功时清零。
	ConsecutiveFailures uint32
}

func (c *Counts) onRequest() { c.Requests++ }

func (c *Counts) onSuccess() {
	c.TotalSuccesses++
	c.ConsecutiveSuccesses++
	c.ConsecutiveFailures = 0
}

func (c *Counts) onFailure() {
	c.TotalFailures++
	c.ConsecutiveFailures++
	c.ConsecutiveSuccesses = 0
}

// Config 是熔断器配置；零值经 New 归一后可安全使用。
type Config struct {
	// Name 用于日志/指标区分实例；空值归一为 "default"。
	Name string
	// MaxRequests 是 HalfOpen 状态允许的并发探测数；0 归一为 1。
	MaxRequests uint32
	// Interval 是 Closed 状态统计窗口长度；0 表示不按时间重置统计。
	// 负值非法。
	Interval time.Duration
	// Timeout 是 Open 状态持续时间，超时后进入 HalfOpen；0 归一为 60s。
	// 负值非法。
	Timeout time.Duration
	// ReadyToTrip 根据 Counts 判断是否从 Closed 打开；nil 时使用 DefaultReadyToTrip。
	// 该回调在 Breaker 锁内调用，禁止反向调用本 Breaker 的方法。
	ReadyToTrip func(Counts) bool
	// IsSuccessful 判断一次调用是否成功；nil 时仅 err == nil 视为成功。
	IsSuccessful func(error) bool
	// OnStateChange 在状态迁移后调用（锁外，可能并发）；仅用于日志/指标。
	OnStateChange func(name string, from, to State)
	// Clock 可注入时钟；nil 时使用系统时钟。
	Clock clock.Clock
}

// DefaultConfig 返回归一后的默认配置。
func DefaultConfig() Config {
	return Config{
		Name:         defaultName,
		MaxRequests:  defaultMaxRequests,
		Timeout:      defaultTimeout,
		ReadyToTrip:  DefaultReadyToTrip,
		IsSuccessful: DefaultIsSuccessful,
		Clock:        clock.RealClock(),
	}
}

// DefaultReadyToTrip 在连续失败达到 5 次时打开熔断器。
func DefaultReadyToTrip(c Counts) bool { return c.ConsecutiveFailures >= defaultFailures }

// DefaultIsSuccessful 仅将 nil error 视为成功。
func DefaultIsSuccessful(err error) bool { return err == nil }

func (c Config) normalize() (Config, error) {
	if c.Interval < 0 || c.Timeout < 0 {
		return Config{}, ErrInvalidConfig
	}
	if c.Name == "" {
		c.Name = defaultName
	}
	if c.MaxRequests == 0 {
		c.MaxRequests = defaultMaxRequests
	}
	if c.Timeout == 0 {
		c.Timeout = defaultTimeout
	}
	if c.ReadyToTrip == nil {
		c.ReadyToTrip = DefaultReadyToTrip
	}
	if c.IsSuccessful == nil {
		c.IsSuccessful = DefaultIsSuccessful
	}
	if c.Clock == nil {
		c.Clock = clock.RealClock()
	}
	return c, nil
}
