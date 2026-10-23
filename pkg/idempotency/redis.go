package idempotency

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/admin-frame/admin-frame/pkg/redis"
)

// redisKeyPrefix 是幂等记录的键域段，实际键还会叠加客户端配置前缀。
const redisKeyPrefix = "idem:"

// RedisStore 是基于 Redis 的跨副本幂等存储。
//
// 所有状态迁移由 Lua 原子完成：同键并发 Begin 仅一个 created=true，
// 指纹不一致立即冲突，Completed 记录可跨副本回放。
type RedisStore struct {
	client *redis.Client
}

// NewRedisStore 构造 Redis 存储；客户端未配置时返回 ErrStoreUnavailable。
func NewRedisStore(client *redis.Client) (*RedisStore, error) {
	if client == nil || client.Redis() == nil {
		return nil, ErrStoreUnavailable
	}
	return &RedisStore{client: client}, nil
}

// Begin 实现 Store。
func (s *RedisStore) Begin(ctx context.Context, key, fingerprint string, ttl time.Duration) (Record, bool, error) {
	if s == nil || s.client == nil {
		return Record{}, false, ErrStoreUnavailable
	}
	if key == "" || fingerprint == "" {
		return Record{}, false, ErrInvalidKey
	}
	ttl = normalizeTTL(ttl)
	record := Record{
		Key:         key,
		Fingerprint: fingerprint,
		State:       StateInProgress,
		ExpiresAt:   time.Now().UTC().Add(ttl),
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return Record{}, false, ErrStoreUnavailable
	}
	result, err := beginScript.Run(ctx, s.client.Redis(), []string{s.fullKey(key)},
		fingerprint, payload, ttl.Milliseconds(), int(StateCompleted)).Result()
	if err != nil {
		return Record{}, false, ErrStoreUnavailable
	}
	code, raw, err := parseScriptResult(result)
	if err != nil {
		return Record{}, false, ErrStoreUnavailable
	}
	switch code {
	case beginCreated:
		return record.Clone(), true, nil
	case beginCompleted:
		existing, decodeErr := decodeRecord(raw)
		if decodeErr != nil {
			return Record{}, false, ErrStoreUnavailable
		}
		return existing.Clone(), false, nil
	case beginConflict:
		return Record{}, false, ErrConflict
	default:
		return Record{}, false, ErrInProgress
	}
}

// Complete 实现 Store。
func (s *RedisStore) Complete(ctx context.Context, key, fingerprint string, statusCode int, header http.Header, body []byte, ttl time.Duration) error {
	if s == nil || s.client == nil {
		return ErrStoreUnavailable
	}
	ttl = normalizeTTL(ttl)
	record := Record{
		Key:         key,
		Fingerprint: fingerprint,
		State:       StateCompleted,
		StatusCode:  statusCode,
		Header:      filterHeaders(header),
		Body:        append([]byte(nil), body...),
		ExpiresAt:   time.Now().UTC().Add(ttl),
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return ErrStoreUnavailable
	}
	result, err := completeScript.Run(ctx, s.client.Redis(), []string{s.fullKey(key)},
		fingerprint, payload, int(StateInProgress), ttl.Milliseconds()).Result()
	if err != nil {
		return ErrStoreUnavailable
	}
	code, _, err := parseScriptResult(result)
	if err != nil {
		return ErrStoreUnavailable
	}
	if code != 0 {
		return ErrConflict
	}
	return nil
}

// Release 实现 Store。
func (s *RedisStore) Release(ctx context.Context, key, fingerprint string) error {
	if s == nil || s.client == nil {
		return ErrStoreUnavailable
	}
	result, err := releaseScript.Run(ctx, s.client.Redis(), []string{s.fullKey(key)},
		fingerprint, int(StateInProgress)).Result()
	if err != nil {
		return ErrStoreUnavailable
	}
	code, _, err := parseScriptResult(result)
	if err != nil {
		return ErrStoreUnavailable
	}
	if code != 0 {
		return ErrConflict
	}
	return nil
}

func (s *RedisStore) fullKey(key string) string {
	return s.client.FullKey(redisKeyPrefix + key)
}

// 脚本返回码：0=成功/新建，1=命中已完成，2=指纹冲突，3=处理中。
const (
	beginCreated   = int64(0)
	beginCompleted = int64(1)
	beginConflict  = int64(2)
)

var beginScript = goredis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if raw then
  local rec = cjson.decode(raw)
  if rec.fingerprint ~= ARGV[1] then
    return {2, ''}
  end
  if rec.state == tonumber(ARGV[4]) then
    return {1, raw}
  end
  return {3, ''}
end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
return {0, ''}
`)

var completeScript = goredis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then
  return {2, ''}
end
local rec = cjson.decode(raw)
if rec.fingerprint ~= ARGV[1] or rec.state ~= tonumber(ARGV[3]) then
  return {2, ''}
end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[4])
return {0, ''}
`)

var releaseScript = goredis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then
  return {0, ''}
end
local rec = cjson.decode(raw)
if rec.fingerprint ~= ARGV[1] then
  return {2, ''}
end
if rec.state == tonumber(ARGV[2]) then
  redis.call('DEL', KEYS[1])
end
return {0, ''}
`)

// parseScriptResult 解析 Lua 的 {code, payload} 返回值。
func parseScriptResult(result any) (code int64, payload string, err error) {
	values, ok := result.([]any)
	if !ok || len(values) < 2 {
		return 0, "", fmt.Errorf("idempotency: unexpected redis script result")
	}
	parsedCode, ok := values[0].(int64)
	if !ok {
		return 0, "", fmt.Errorf("idempotency: unexpected redis script code")
	}
	raw, _ := values[1].(string)
	return parsedCode, raw, nil
}

func decodeRecord(raw string) (Record, error) {
	var record Record
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return Record{}, fmt.Errorf("idempotency: decode redis record: %w", err)
	}
	return record, nil
}

func normalizeTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return defaultTTL
	}
	if ttl < time.Millisecond {
		return time.Millisecond
	}
	return ttl
}
