package cache

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/authz"
)

// SchemaVersion 是缓存值结构版本；修改字段语义必须递增，旧值按 miss 处理。
const SchemaVersion = 1

// envelope 是落缓存的稳定结构：字段名与顺序即跨版本契约，禁止随域模型重命名。
type envelope struct {
	SchemaVersion int                    `json:"schema_version"`
	Channel       string                 `json:"channel"`
	SubjectType   string                 `json:"subject_type"`
	SubjectID     string                 `json:"subject_id"`
	MerchantID    string                 `json:"merchant_id"`
	Roles         []string               `json:"roles"`
	Permissions   []string               `json:"permissions"`
	Scopes        map[string]authz.Scope `json:"scopes,omitempty"`
	Version       int64                  `json:"version"`
	GeneratedAt   time.Time              `json:"generated_at"`
}

// Encode 把快照序列化为带 schema_version 的 JSON；切片 / 映射为空时写空集合而非 null。
func Encode(snapshot authz.Snapshot) ([]byte, error) {
	env := envelope{
		SchemaVersion: SchemaVersion,
		Channel:       snapshot.Channel,
		SubjectType:   snapshot.SubjectType,
		SubjectID:     snapshot.SubjectID,
		MerchantID:    snapshot.MerchantID,
		Roles:         append([]string{}, snapshot.Roles...),
		Permissions:   append([]string{}, snapshot.Permissions...),
		Scopes:        snapshot.Scopes,
		Version:       snapshot.Version,
		GeneratedAt:   snapshot.GeneratedAt.UTC(),
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("iam cache: encode snapshot: %w", err)
	}
	return raw, nil
}

// Decode 反序列化缓存值。
//
// 任何结构 / 版本不符都返回 ErrCorrupt，调用方必须视为 miss 并回源，
// 不得 panic，也不得把残缺快照当成「无权限」或「全权限」。
func Decode(raw []byte) (authz.Snapshot, error) {
	if len(raw) == 0 {
		return authz.Snapshot{}, fmt.Errorf("%w: empty payload", ErrCorrupt)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return authz.Snapshot{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	if env.SchemaVersion != SchemaVersion {
		return authz.Snapshot{}, fmt.Errorf("%w: schema_version=%d want %d", ErrCorrupt, env.SchemaVersion, SchemaVersion)
	}
	if env.Channel == "" || env.SubjectID == "" {
		return authz.Snapshot{}, fmt.Errorf("%w: channel and subject_id are required", ErrCorrupt)
	}
	return authz.Snapshot{
		Channel:     env.Channel,
		SubjectType: env.SubjectType,
		SubjectID:   env.SubjectID,
		MerchantID:  env.MerchantID,
		Roles:       append([]string(nil), env.Roles...),
		Permissions: append([]string(nil), env.Permissions...),
		Scopes:      cloneScopes(env.Scopes),
		Version:     env.Version,
		GeneratedAt: env.GeneratedAt.UTC(),
	}, nil
}

func cloneScopes(scopes map[string]authz.Scope) map[string]authz.Scope {
	if len(scopes) == 0 {
		return nil
	}
	out := make(map[string]authz.Scope, len(scopes))
	for resource, scope := range scopes {
		out[resource] = scope.Clone()
	}
	return out
}
