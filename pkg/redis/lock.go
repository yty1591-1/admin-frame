package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// unlockScript 仅当 value 匹配时删除，防止误删他人锁。
var unlockScript = goredis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
else
	return 0
end
`)

// renewScript 仅当 value 匹配时续期。
var renewScript = goredis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("PEXPIRE", KEYS[1], ARGV[2])
else
	return 0
end
`)

// Locker 基于单 Redis 实例的互斥锁工厂（SET key token NX PX ttl）。
type Locker struct {
	client *Client
}

// Acquire 尝试获取锁；ttl 必须 >0。失败返回 ErrNotObtained（已被占用）或其他错误。
//
// 持有者须在临界区结束后 Unlock；建议 defer。进程崩溃依赖 TTL 自动释放。
func (l *Locker) Acquire(ctx context.Context, name string, ttl time.Duration) (*Lock, error) {
	if l == nil || l.client == nil || l.client.rdb == nil || l.client.closed.Load() {
		return nil, ErrClosed
	}
	if name == "" {
		return nil, fmt.Errorf("%w: lock name is required", ErrInvalidConfig)
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("%w: lock ttl must be > 0", ErrInvalidConfig)
	}

	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	key := l.client.FullKey(JoinPrefix("lock", name))
	ok, err := l.client.rdb.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: lock acquire: %w", err)
	}
	if !ok {
		return nil, ErrNotObtained
	}
	return &Lock{client: l.client, key: key, token: token}, nil
}

// Lock 表示已持有的互斥锁。
type Lock struct {
	client *Client
	key    string
	token  string
}

// Key 返回锁在 Redis 中的完整 key（含前缀）。
func (l *Lock) Key() string {
	if l == nil {
		return ""
	}
	return l.key
}

// Unlock 释放锁；非持有者调用返回 ErrLockNotHeld。
func (l *Lock) Unlock(ctx context.Context) error {
	if l == nil || l.client == nil || l.client.rdb == nil || l.client.closed.Load() {
		return ErrClosed
	}
	n, err := unlockScript.Run(ctx, l.client.rdb, []string{l.key}, l.token).Int()
	if err != nil {
		return fmt.Errorf("redis: lock unlock: %w", err)
	}
	if n == 0 {
		return ErrLockNotHeld
	}
	return nil
}

// Renew 将锁 TTL 重置为 ttl（须仍持有）；用于长临界区续命。
func (l *Lock) Renew(ctx context.Context, ttl time.Duration) error {
	if l == nil || l.client == nil || l.client.rdb == nil || l.client.closed.Load() {
		return ErrClosed
	}
	if ttl <= 0 {
		return fmt.Errorf("%w: lock ttl must be > 0", ErrInvalidConfig)
	}
	ms := ttl.Milliseconds()
	if ms <= 0 {
		return fmt.Errorf("%w: lock ttl too small", ErrInvalidConfig)
	}
	n, err := renewScript.Run(ctx, l.client.rdb, []string{l.key}, l.token, ms).Int()
	if err != nil {
		return fmt.Errorf("redis: lock renew: %w", err)
	}
	if n == 0 {
		return ErrLockNotHeld
	}
	return nil
}

func randomToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("redis: lock token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
