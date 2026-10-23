package cache

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/redis"
)

// 短信验证码缓存键前缀（与 docs/identity-authn.md §8.1 一致）。
const (
	smsCodeKeyPrefix    = "sms:code:"
	smsAttemptKeyPrefix = "sms:attempt:"
)

// smsCodeEntry 是进程内验证码条目。
type smsCodeEntry struct {
	hash      string
	expiresAt time.Time
}

// smsAttemptEntry 是进程内错误次数条目。
type smsAttemptEntry struct {
	count     int
	expiresAt time.Time
}

// MemorySMSCodeStore 是进程内的验证码存储，仅用于单副本 / 本地 / 单测。
type MemorySMSCodeStore struct {
	mu       sync.Mutex
	codes    map[string]smsCodeEntry
	attempts map[string]smsAttemptEntry
	now      func() time.Time
}

// 编译期断言。
var _ app.SMSCodeStore = (*MemorySMSCodeStore)(nil)

// NewMemorySMSCodeStore 创建空的验证码存储；clock 为 nil 时使用系统时钟。
func NewMemorySMSCodeStore(clock app.Clock) *MemorySMSCodeStore {
	now := systemNow
	if clock != nil {
		now = clock.Now
	}
	return &MemorySMSCodeStore{
		codes:    make(map[string]smsCodeEntry),
		attempts: make(map[string]smsAttemptEntry),
		now:      now,
	}
}

// Save 写入验证码摘要；同手机号覆盖旧值并重置 TTL。
func (s *MemorySMSCodeStore) Save(_ context.Context, phoneHash, codeHash string, ttl time.Duration) error {
	if err := validateSMSKey(phoneHash); err != nil {
		return err
	}
	if strings.TrimSpace(codeHash) == "" {
		return apperr.BadRequest("sms code hash is required")
	}
	if ttl <= 0 {
		return apperr.BadRequest("sms code ttl must be > 0")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[phoneHash] = smsCodeEntry{hash: codeHash, expiresAt: s.now().Add(ttl)}
	return nil
}

// Get 返回验证码摘要；不存在或已过期返回 NotFound。
func (s *MemorySMSCodeStore) Get(_ context.Context, phoneHash string) (string, error) {
	if err := validateSMSKey(phoneHash); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.codes[phoneHash]
	if !ok {
		return "", apperr.NotFound("sms code not found")
	}
	if !s.now().Before(entry.expiresAt) {
		delete(s.codes, phoneHash)
		return "", apperr.NotFound("sms code not found")
	}
	return entry.hash, nil
}

// Delete 删除验证码；不存在也返回成功。
func (s *MemorySMSCodeStore) Delete(_ context.Context, phoneHash string) error {
	if err := validateSMSKey(phoneHash); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.codes, phoneHash)
	return nil
}

// IncrAttempts 递增错误次数并返回当前值；首次写入设置 TTL。
func (s *MemorySMSCodeStore) IncrAttempts(_ context.Context, phoneHash string, ttl time.Duration) (int, error) {
	if err := validateSMSKey(phoneHash); err != nil {
		return 0, err
	}
	if ttl <= 0 {
		return 0, apperr.BadRequest("sms code ttl must be > 0")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	entry, ok := s.attempts[phoneHash]
	if !ok || !now.Before(entry.expiresAt) {
		entry = smsAttemptEntry{expiresAt: now.Add(ttl)}
	}
	entry.count++
	s.attempts[phoneHash] = entry
	return entry.count, nil
}

// ResetAttempts 清除错误次数。
func (s *MemorySMSCodeStore) ResetAttempts(_ context.Context, phoneHash string) error {
	if err := validateSMSKey(phoneHash); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.attempts, phoneHash)
	return nil
}

// RedisSMSCodeStore 基于 Redis 实现跨副本的验证码与错误次数存储。
type RedisSMSCodeStore struct {
	client *redis.Cache
}

// 编译期断言。
var _ app.SMSCodeStore = (*RedisSMSCodeStore)(nil)

// NewRedisSMSCodeStore 构造 Redis 验证码存储；client 为 nil 时操作失败关闭。
func NewRedisSMSCodeStore(client *redis.Cache) *RedisSMSCodeStore {
	return &RedisSMSCodeStore{client: client}
}

// Save 写入验证码摘要。
func (s *RedisSMSCodeStore) Save(ctx context.Context, phoneHash, codeHash string, ttl time.Duration) error {
	if err := validateSMSKey(phoneHash); err != nil {
		return err
	}
	if strings.TrimSpace(codeHash) == "" {
		return apperr.BadRequest("sms code hash is required")
	}
	if s.client == nil {
		return apperr.Unavailable("sms code store is not configured")
	}
	if err := s.client.Set(ctx, smsCodeKeyPrefix+phoneHash, []byte(codeHash), ttl); err != nil {
		return fmt.Errorf("identity sms code save: %w", err)
	}
	return nil
}

// Get 返回验证码摘要；不存在返回 NotFound。
func (s *RedisSMSCodeStore) Get(ctx context.Context, phoneHash string) (string, error) {
	if err := validateSMSKey(phoneHash); err != nil {
		return "", err
	}
	if s.client == nil {
		return "", apperr.Unavailable("sms code store is not configured")
	}
	value, err := s.client.Get(ctx, smsCodeKeyPrefix+phoneHash)
	if err != nil {
		if errors.Is(err, redis.ErrMiss) {
			return "", apperr.NotFound("sms code not found")
		}
		return "", fmt.Errorf("identity sms code get: %w", err)
	}
	return string(value), nil
}

// Delete 删除验证码；不存在也返回成功。
func (s *RedisSMSCodeStore) Delete(ctx context.Context, phoneHash string) error {
	if err := validateSMSKey(phoneHash); err != nil {
		return err
	}
	if s.client == nil {
		return apperr.Unavailable("sms code store is not configured")
	}
	if err := s.client.Delete(ctx, smsCodeKeyPrefix+phoneHash); err != nil {
		return fmt.Errorf("identity sms code delete: %w", err)
	}
	return nil
}

// IncrAttempts 原子递增错误次数；首次写入设置 TTL。
func (s *RedisSMSCodeStore) IncrAttempts(ctx context.Context, phoneHash string, ttl time.Duration) (int, error) {
	if err := validateSMSKey(phoneHash); err != nil {
		return 0, err
	}
	if s.client == nil {
		return 0, apperr.Unavailable("sms code store is not configured")
	}
	value, err := s.client.Incr(ctx, smsAttemptKeyPrefix+phoneHash, ttl)
	if err != nil {
		return 0, fmt.Errorf("identity sms attempts incr: %w", err)
	}
	return int(value), nil
}

// ResetAttempts 清除错误次数。
func (s *RedisSMSCodeStore) ResetAttempts(ctx context.Context, phoneHash string) error {
	if err := validateSMSKey(phoneHash); err != nil {
		return err
	}
	if s.client == nil {
		return apperr.Unavailable("sms code store is not configured")
	}
	if err := s.client.Delete(ctx, smsAttemptKeyPrefix+phoneHash); err != nil {
		return fmt.Errorf("identity sms attempts reset: %w", err)
	}
	return nil
}

// validateSMSKey 校验手机号摘要非空。
func validateSMSKey(phoneHash string) error {
	if strings.TrimSpace(phoneHash) == "" {
		return apperr.BadRequest("phone hash is required")
	}
	return nil
}

// systemNow 是默认时钟实现。
func systemNow() time.Time { return time.Now().UTC() }
