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
	// sessionKeyPrefix 会话撤销快照键：authn:sid:<session_id>。
	sessionKeyPrefix = "authn:sid:"
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

// LoadSessionState 读取会话撤销快照；未命中或内容损坏返回 found=false。
func (s *RedisRevocationStore) LoadSessionState(ctx context.Context, sessionID string) (app.SessionState, bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return app.SessionState{}, false, apperr.BadRequest("session id is required")
	}
	raw, err := s.cache.Get(ctx, sessionKeyPrefix+sessionID)
	if err != nil {
		if errors.Is(err, redis.ErrMiss) {
			return app.SessionState{}, false, nil
		}
		return app.SessionState{}, false, fmt.Errorf("identity session revocation get: %w", err)
	}
	state, err := decodeSessionState(raw)
	if err != nil {
		// 缓存内容损坏时按未命中处理：回源数据库并回填，而不是让鉴权整体失败。
		return app.SessionState{}, false, nil
	}
	return state, true, nil
}

// SaveSessionState 写入会话撤销快照。
func (s *RedisRevocationStore) SaveSessionState(ctx context.Context, sessionID string, state app.SessionState, ttl time.Duration) error {
	if strings.TrimSpace(sessionID) == "" {
		return apperr.BadRequest("session id is required")
	}
	value := encodeSessionState(state)
	if err := s.cache.Set(ctx, sessionKeyPrefix+sessionID, []byte(value), ttl); err != nil {
		return fmt.Errorf("identity session revocation set: %w", err)
	}
	return nil
}

// DeleteSessionState 删除会话撤销快照；用于写入失败后的失效兜底。
func (s *RedisRevocationStore) DeleteSessionState(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return apperr.BadRequest("session id is required")
	}
	if err := s.cache.Delete(ctx, sessionKeyPrefix+sessionID); err != nil {
		return fmt.Errorf("identity session revocation delete: %w", err)
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

// encodeSessionState 把会话快照编码为 "<status>|<revoked_at_ms>|<expires_at_ms>|<fetched_at_ms>"。
//
// 零值时间统一写 0，避免 time.Time 的零值在不同版本序列化出不同结果。
func encodeSessionState(state app.SessionState) string {
	return fmt.Sprintf("%s|%d|%d|%d",
		state.Status,
		unixMilli(state.RevokedAt),
		unixMilli(state.ExpiresAt),
		unixMilli(state.FetchedAt),
	)
}

// unixMilli 把时间转换为毫秒；零值返回 0。
func unixMilli(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.UnixMilli()
}

// decodeSessionState 解析会话快照；缺少 fetched_at 视为陈旧，由检查器回源升级。
func decodeSessionState(raw []byte) (app.SessionState, error) {
	parts := strings.SplitN(string(raw), "|", 4)
	if len(parts) < 4 {
		return app.SessionState{}, fmt.Errorf("identity session revocation: malformed state")
	}
	status := domain.SessionStatus(parts[0])
	if err := status.Validate(); err != nil {
		return app.SessionState{}, err
	}
	var times [3]time.Time
	for i, part := range parts[1:] {
		ms, err := strconv.ParseInt(part, 10, 64)
		if err != nil || ms < 0 {
			return app.SessionState{}, fmt.Errorf("identity session revocation: malformed time")
		}
		if ms > 0 {
			times[i] = time.UnixMilli(ms).UTC()
		}
	}
	return app.SessionState{
		Status:    status,
		RevokedAt: times[0],
		ExpiresAt: times[1],
		FetchedAt: times[2],
	}, nil
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
