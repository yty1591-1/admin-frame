// Package cache 提供身份域的撤销 / 验证码等短期状态存储。
//
// 本包只依赖 pkg/redis 与应用层端口，不感知 HTTP 与用例编排。
package cache

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/redis"
)

// Redis 键前缀（docs/identity-authn.md §6.1）。
const (
	// stateKeyPrefix 账号撤销快照键：authn:ver:<account_id>。
	stateKeyPrefix = "authn:ver:"
	// revokedKeyPrefix jti 黑名单键：authn:revoked:<jti>。
	revokedKeyPrefix = "authn:revoked:"
)

// RedisRevocationStore 基于 pkg/redis.Cache 实现撤销存储。
//
// 键统一加客户端 KeyPrefix；Cache 自身再追加 cache: 段，跨环境互不影响。
type RedisRevocationStore struct {
	cache *redis.Cache
}

// 编译期断言。
var _ app.RevocationStore = (*RedisRevocationStore)(nil)

// NewRedisRevocationStore 构造 Redis 撤销存储；cache 为 nil 时所有操作失败关闭。
func NewRedisRevocationStore(c *redis.Cache) *RedisRevocationStore {
	return &RedisRevocationStore{cache: c}
}

// LoadAccountState 读取账号撤销快照；未命中返回 found=false。
func (s *RedisRevocationStore) LoadAccountState(ctx context.Context, accountID string) (app.AccountState, bool, error) {
	if strings.TrimSpace(accountID) == "" {
		return app.AccountState{}, false, apperr.BadRequest("account id is required")
	}
	raw, err := s.cache.Get(ctx, stateKeyPrefix+accountID)
	if err != nil {
		if errors.Is(err, redis.ErrMiss) {
			return app.AccountState{}, false, nil
		}
		return app.AccountState{}, false, fmt.Errorf("identity revocation get: %w", err)
	}
	state, err := decodeState(raw)
	if err != nil {
		// 缓存内容损坏时按未命中处理：回源数据库并回填，而不是让鉴权整体失败。
		return app.AccountState{}, false, nil
	}
	return state, true, nil
}

// SaveAccountState 写入账号撤销快照。
func (s *RedisRevocationStore) SaveAccountState(ctx context.Context, accountID string, state app.AccountState, ttl time.Duration) error {
	if strings.TrimSpace(accountID) == "" {
		return apperr.BadRequest("account id is required")
	}
	value := fmt.Sprintf("%d|%s|%d", state.Version, state.Status, state.FetchedAt.UnixMilli())
	if err := s.cache.Set(ctx, stateKeyPrefix+accountID, []byte(value), ttl); err != nil {
		return fmt.Errorf("identity revocation set: %w", err)
	}
	return nil
}

// DeleteAccountState 删除账号撤销快照；用于写入失败后的失效兜底。
func (s *RedisRevocationStore) DeleteAccountState(ctx context.Context, accountID string) error {
	if strings.TrimSpace(accountID) == "" {
		return apperr.BadRequest("account id is required")
	}
	if err := s.cache.Delete(ctx, stateKeyPrefix+accountID); err != nil {
		return fmt.Errorf("identity revocation delete: %w", err)
	}
	return nil
}

// RevokeToken 把 jti 写入黑名单，ttl 为令牌剩余寿命。
func (s *RedisRevocationStore) RevokeToken(ctx context.Context, jti string, ttl time.Duration) error {
	if strings.TrimSpace(jti) == "" {
		return apperr.BadRequest("jti is required")
	}
	if err := s.cache.Set(ctx, revokedKeyPrefix+jti, []byte("1"), ttl); err != nil {
		return fmt.Errorf("identity revocation mark: %w", err)
	}
	return nil
}

// IsTokenRevoked 报告 jti 是否在黑名单中；未命中按未撤销处理。
func (s *RedisRevocationStore) IsTokenRevoked(ctx context.Context, jti string) (bool, error) {
	if strings.TrimSpace(jti) == "" {
		return false, nil
	}
	if _, err := s.cache.Get(ctx, revokedKeyPrefix+jti); err != nil {
		if errors.Is(err, redis.ErrMiss) {
			return false, nil
		}
		return false, fmt.Errorf("identity revocation check: %w", err)
	}
	return true, nil
}

// decodeState 解析 "<version>|<status>|<fetched_at_ms>" 快照。
//
// 兼容旧格式 "<version>|<status>"：FetchedAt 留零值，由检查器判定为陈旧并回源刷新。
func decodeState(raw []byte) (app.AccountState, error) {
	parts := strings.SplitN(string(raw), "|", 3)
	if len(parts) < 2 {
		return app.AccountState{}, fmt.Errorf("identity revocation: malformed state")
	}
	version, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || version < 1 {
		return app.AccountState{}, fmt.Errorf("identity revocation: malformed version")
	}
	status := domain.AccountStatus(parts[1])
	if err := status.Validate(); err != nil {
		return app.AccountState{}, err
	}
	state := app.AccountState{Version: version, Status: status}
	if len(parts) == 3 {
		ms, parseErr := strconv.ParseInt(parts[2], 10, 64)
		if parseErr != nil || ms < 0 {
			return app.AccountState{}, fmt.Errorf("identity revocation: malformed fetched_at")
		}
		state.FetchedAt = time.UnixMilli(ms).UTC()
	}
	return state, nil
}
