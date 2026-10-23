package cache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/redis"
)

// 短信防刷缓存键前缀。
const (
	smsResendKeyPrefix = "sms:resend:"
	smsDailyKeyPrefix  = "sms:daily:"
	smsIPKeyPrefix     = "sms:ip:"
)

// 计数窗口时长：日限与小时限各留少量余量，避免窗口边界丢计数。
const (
	smsDailyWindowTTL  = 25 * time.Hour
	smsHourlyWindowTTL = 65 * time.Minute
)

// MemorySMSRateLimiter 是进程内的短信防刷实现，仅用于单副本 / 本地 / 单测。
type MemorySMSRateLimiter struct {
	mu     sync.Mutex
	resend map[string]time.Time
	daily  map[string]smsCounter
	hourly map[string]smsCounter
	now    func() time.Time
}

// smsCounter 是带过期时间的计数器。
type smsCounter struct {
	count     int
	expiresAt time.Time
}

// 编译期断言。
var _ app.SMSRateLimiter = (*MemorySMSRateLimiter)(nil)

// NewMemorySMSRateLimiter 创建空的防刷存储；clock 为 nil 时使用系统时钟。
func NewMemorySMSRateLimiter(clock app.Clock) *MemorySMSRateLimiter {
	now := systemNow
	if clock != nil {
		now = clock.Now
	}
	return &MemorySMSRateLimiter{
		resend: make(map[string]time.Time),
		daily:  make(map[string]smsCounter),
		hourly: make(map[string]smsCounter),
		now:    now,
	}
}

// AllowResend 校验最小重发间隔；放行时记录本次发送时间。
func (l *MemorySMSRateLimiter) AllowResend(_ context.Context, phoneHash string, interval time.Duration) (bool, time.Duration, error) {
	if err := validateSMSKey(phoneHash); err != nil {
		return false, 0, err
	}
	if interval <= 0 {
		return true, 0, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if last, ok := l.resend[phoneHash]; ok {
		if elapsed := now.Sub(last); elapsed < interval {
			return false, interval - elapsed, nil
		}
	}
	l.resend[phoneHash] = now
	return true, 0, nil
}

// AllowDaily 校验单手机号日发送上限。
func (l *MemorySMSRateLimiter) AllowDaily(_ context.Context, phoneHash string, limit int) (bool, error) {
	if err := validateSMSKey(phoneHash); err != nil {
		return false, err
	}
	if limit < 1 {
		return false, apperr.BadRequest("sms daily limit must be >= 1")
	}
	return l.allow(l.daily, dayKey(l.now(), phoneHash), limit, smsDailyWindowTTL), nil
}

// AllowHourlyIP 校验单 IP 小时发送上限。
func (l *MemorySMSRateLimiter) AllowHourlyIP(_ context.Context, ipHash string, limit int) (bool, error) {
	if err := validateSMSKey(ipHash); err != nil {
		return false, err
	}
	if limit < 1 {
		return false, apperr.BadRequest("sms hourly limit must be >= 1")
	}
	return l.allow(l.hourly, hourKey(l.now(), ipHash), limit, smsHourlyWindowTTL), nil
}

// allow 在旧窗口过期后重置计数，并判断是否仍在限额内。
func (l *MemorySMSRateLimiter) allow(store map[string]smsCounter, key string, limit int, ttl time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	entry, ok := store[key]
	if !ok || !now.Before(entry.expiresAt) {
		entry = smsCounter{expiresAt: now.Add(ttl)}
	}
	if entry.count >= limit {
		store[key] = entry
		return false
	}
	entry.count++
	store[key] = entry
	return true
}

// RedisSMSRateLimiter 基于 Redis 实现跨副本的短信防刷。
type RedisSMSRateLimiter struct {
	client *redis.Cache
	now    func() time.Time
}

// 编译期断言。
var _ app.SMSRateLimiter = (*RedisSMSRateLimiter)(nil)

// NewRedisSMSRateLimiter 构造 Redis 防刷存储；client 为 nil 时操作失败关闭。
func NewRedisSMSRateLimiter(client *redis.Cache, clock app.Clock) *RedisSMSRateLimiter {
	now := systemNow
	if clock != nil {
		now = clock.Now
	}
	return &RedisSMSRateLimiter{client: client, now: now}
}

// AllowResend 使用 SETNX 抢占重发窗口；未抢到视为过于频繁。
func (l *RedisSMSRateLimiter) AllowResend(ctx context.Context, phoneHash string, interval time.Duration) (bool, time.Duration, error) {
	if err := validateSMSKey(phoneHash); err != nil {
		return false, 0, err
	}
	if interval <= 0 {
		return true, 0, nil
	}
	if l.client == nil {
		return false, 0, apperr.Unavailable("sms rate limiter is not configured")
	}
	ok, err := l.client.SetNX(ctx, smsResendKeyPrefix+phoneHash, []byte("1"), interval)
	if err != nil {
		return false, 0, fmt.Errorf("identity sms resend limit: %w", err)
	}
	if !ok {
		return false, interval, nil
	}
	return true, 0, nil
}

// AllowDaily 校验单手机号日发送上限。
func (l *RedisSMSRateLimiter) AllowDaily(ctx context.Context, phoneHash string, limit int) (bool, error) {
	if err := validateSMSKey(phoneHash); err != nil {
		return false, err
	}
	if limit < 1 {
		return false, apperr.BadRequest("sms daily limit must be >= 1")
	}
	return l.allow(ctx, smsDailyKeyPrefix+dayKey(l.now(), phoneHash), limit, smsDailyWindowTTL)
}

// AllowHourlyIP 校验单 IP 小时发送上限。
func (l *RedisSMSRateLimiter) AllowHourlyIP(ctx context.Context, ipHash string, limit int) (bool, error) {
	if err := validateSMSKey(ipHash); err != nil {
		return false, err
	}
	if limit < 1 {
		return false, apperr.BadRequest("sms hourly limit must be >= 1")
	}
	return l.allow(ctx, smsIPKeyPrefix+hourKey(l.now(), ipHash), limit, smsHourlyWindowTTL)
}

// allow 原子自增并判断是否超过限额。
func (l *RedisSMSRateLimiter) allow(ctx context.Context, key string, limit int, ttl time.Duration) (bool, error) {
	if l.client == nil {
		return false, apperr.Unavailable("sms rate limiter is not configured")
	}
	value, err := l.client.Incr(ctx, key, ttl)
	if err != nil {
		return false, fmt.Errorf("identity sms rate limit: %w", err)
	}
	return int(value) <= limit, nil
}

// dayKey 返回按 UTC 日期分桶的键后缀。
func dayKey(now time.Time, hash string) string {
	return now.UTC().Format("2006-01-02") + ":" + hash
}

// hourKey 返回按 UTC 小时分桶的键后缀。
func hourKey(now time.Time, hash string) string {
	return now.UTC().Format("2006-01-02-15") + ":" + hash
}
