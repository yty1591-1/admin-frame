package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/admin-frame/admin-frame/pkg/redis"
)

// Redis 故障策略。
const (
	// RedisFailureOpen Redis 不可用时放行（可用性优先）。
	RedisFailureOpen = "fail_open"
	// RedisFailureClosed Redis 不可用时拒绝（安全优先）。
	RedisFailureClosed = "fail_closed"
)

const defaultRedisKeyPrefix = "ratelimit:"

// RedisKeyedConfig 配置跨副本共享的令牌桶。
type RedisKeyedConfig struct {
	// RPS 每秒补充令牌数；须 >0。
	RPS float64
	// Burst 桶容量；须 ≥1。
	Burst int
	// IdleTTL 键空闲过期时间；≤0 时 10m。
	IdleTTL time.Duration
	// FailureMode fail_open（默认）| fail_closed。
	FailureMode string
	// KeyPrefix 键域段；空则 ratelimit:。
	KeyPrefix string
	// DegradeOnFailure 为 true 时，fail_closed 模式在 Redis 连续失败达到阈值后
	// 进入宽松降级窗口：放行请求并返回 Degraded=true，避免共享存储故障放大为全站不可用（P1-4）。
	DegradeOnFailure bool
	// DegradeAfter 触发降级的连续失败次数；<=0 时 5。
	DegradeAfter int
	// DegradeCooldown 降级窗口；<=0 时 30s；窗口结束后重新探测 Redis。
	DegradeCooldown time.Duration
}

// RedisKeyed 是基于 Redis 的按 key 分桶令牌桶（多副本共享配额）。
//
// 令牌补充与扣减在同一 Lua 脚本内完成，使用 Redis 服务端 TIME，
// 避免各副本本地时钟偏差影响配额。
type RedisKeyed struct {
	client           *redis.Client
	rps              float64
	burst            int
	idleTTL          time.Duration
	failureMode      string
	keyPrefix        string
	degradeOnFailure bool
	degradeAfter     int
	degradeCooldown  time.Duration

	mu            sync.Mutex
	failures      int
	degradedUntil time.Time
}

// NewRedisKeyed 构造 Redis 限流注册表；客户端未配置或参数非法时返回错误。
func NewRedisKeyed(client *redis.Client, cfg RedisKeyedConfig) (*RedisKeyed, error) {
	if client == nil || client.Redis() == nil {
		return nil, fmt.Errorf("ratelimit: redis client is required")
	}
	if cfg.RPS <= 0 || cfg.Burst < 1 {
		return nil, fmt.Errorf("ratelimit: redis requires rps > 0 and burst >= 1")
	}
	mode := cfg.FailureMode
	if mode == "" {
		mode = RedisFailureOpen
	}
	if mode != RedisFailureOpen && mode != RedisFailureClosed {
		return nil, fmt.Errorf("ratelimit: unsupported failure_mode %q", cfg.FailureMode)
	}
	ttl := cfg.IdleTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	prefix := cfg.KeyPrefix
	if prefix == "" {
		prefix = defaultRedisKeyPrefix
	}
	after := cfg.DegradeAfter
	if after <= 0 {
		after = 5
	}
	cooldown := cfg.DegradeCooldown
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &RedisKeyed{
		client:           client,
		rps:              cfg.RPS,
		burst:            cfg.Burst,
		idleTTL:          ttl,
		failureMode:      mode,
		keyPrefix:        prefix,
		degradeOnFailure: cfg.DegradeOnFailure,
		degradeAfter:     after,
		degradeCooldown:  cooldown,
	}, nil
}

// Allow 对 key 执行 AllowN(1)。
func (r *RedisKeyed) Allow(key string) Result { return r.AllowN(key, 1) }

// AllowN 原子扣减 n 个令牌；Redis 故障按 failure_mode 处理。
func (r *RedisKeyed) AllowN(key string, n int) Result {
	return r.AllowNWithParams(key, r.rps, r.burst, n)
}

// AllowNWithParams 以键级参数执行原子扣减：配额场景下每个租户的
// 容量不同（ENT-14），无法在构造期固定 RPS / Burst。其余语义
// （Lua 原子、服务端 TIME、键空闲过期、fail mode）与 AllowN 一致。
//
// 参数非法（rps<=0 / burst<1）时按 onFailure 处理，不 panic。
func (r *RedisKeyed) AllowNWithParams(key string, rps float64, burst, n int) Result {
	if n <= 0 {
		n = 1
	}
	if r == nil || r.client == nil || rps <= 0 || burst < 1 {
		return r.onFailure()
	}
	if key == "" {
		key = "_"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := tokenBucketScript.Run(ctx, r.client.Redis(), []string{r.client.FullKey(r.keyPrefix + key)},
		rps, burst, r.idleTTL.Milliseconds(), n).Result()
	if err != nil {
		return r.onFailure()
	}
	values, ok := result.([]any)
	if !ok || len(values) < 3 {
		return r.onFailure()
	}
	r.onSuccess()
	return decodeTokenBucket(values)
}

// decodeTokenBucket 把 Lua 返回值解析为 Result。
func decodeTokenBucket(values []any) Result {
	allowed, _ := values[0].(int64)
	remaining, _ := values[1].(int64)
	retryRaw, _ := values[2].(string)
	retrySeconds, _ := strconv.ParseFloat(retryRaw, 64)
	out := Result{Allowed: allowed == 1, Remaining: float64(remaining)}
	if !out.Allowed && retrySeconds > 0 {
		out.RetryAfter = time.Duration(retrySeconds * float64(time.Second))
	}
	return out
}

// onSuccess 在 Redis 恢复后清零失败计数并退出降级窗口。
func (r *RedisKeyed) onSuccess() {
	if r == nil || !r.degradeOnFailure {
		return
	}
	r.mu.Lock()
	r.failures = 0
	r.degradedUntil = time.Time{}
	r.mu.Unlock()
}

// onFailure 按 failure_mode 处理 Redis 故障；开启降级时在连续失败后
// 进入宽松窗口，返回 Degraded 标记而不是直接拒绝。
func (r *RedisKeyed) onFailure() Result {
	if r == nil || r.failureMode != RedisFailureClosed {
		return Result{Allowed: true}
	}
	if !r.degradeOnFailure {
		return Result{Allowed: false, RetryAfter: time.Second}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if now.Before(r.degradedUntil) {
		return Result{Allowed: true, Degraded: true}
	}
	r.failures++
	if r.failures >= r.degradeAfter {
		r.failures = 0
		r.degradedUntil = now.Add(r.degradeCooldown)
		return Result{Allowed: true, Degraded: true}
	}
	return Result{Allowed: false, RetryAfter: time.Second}
}

var tokenBucketScript = goredis.NewScript(`
local data = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(data[1])
local ts = tonumber(data[2])
local nowParts = redis.call('TIME')
local now = tonumber(nowParts[1]) + tonumber(nowParts[2]) / 1000000
local capacity = tonumber(ARGV[2])
local rps = tonumber(ARGV[1])
local want = tonumber(ARGV[4])
if tokens == nil then
  tokens = capacity
  ts = now
end
tokens = math.min(capacity, tokens + (now - ts) * rps)
local allowed = 0
local retry = 0
if tokens >= want then
  tokens = tokens - want
  allowed = 1
else
  retry = (want - tokens) / rps
end
redis.call('HMSET', KEYS[1], 'tokens', tokens, 'ts', now)
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return {allowed, math.floor(tokens), tostring(retry)}
`)
