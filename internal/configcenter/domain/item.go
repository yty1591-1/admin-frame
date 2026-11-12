// Package domain 是配置中心域模型（M1）。
//
// 本包只承载纯类型、状态机与校验，不涉 SQL / HTTP / 配置中心客户端：
// 持久化在 infrastructure/persistence，用例在 app（M1 后续里程碑），
// 进程内 SDK 在 pkg/configclient。
//
// 不变量与契约见 docs/config-center-plan.md 附录 A：
//   - env / namespace / key 唯一；
//   - 版本只追加，版本内容不可变；
//   - 发布快照不可变，生效切换只改 config_release_pointer；
//   - 敏感项的值不得进入本域（走 config_secret 子域，M2）。
package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// 长度上限（与迁移 000031 列宽对齐）。
const (
	MaxEnvLength         = 16
	MaxNamespaceLength   = 64
	MaxKeyLength         = 128
	MaxDescriptionLength = 512
	MaxOwnerLength       = 64
	MaxChangeSummary     = 512
	MaxReleaseName       = 128
	MaxReviewComment     = 512
)

var (
	namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	keyPattern       = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	sha256Pattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// 环境枚举；与部署环境一一对应，未登记的环境拒绝写入。
const (
	EnvLocal   = "local"
	EnvDev     = "dev"
	EnvTest    = "test"
	EnvStaging = "staging"
	EnvProd    = "prod"
)

var validEnvs = map[string]struct{}{
	EnvLocal: {}, EnvDev: {}, EnvTest: {}, EnvStaging: {}, EnvProd: {},
}

// ValidEnv 判断环境是否在允许集合内。
func ValidEnv(env string) bool {
	_, ok := validEnvs[env]
	return ok
}

// ValidNamespace 判断命名空间是否为小写字母开头的短横线 / 下划线标识。
func ValidNamespace(namespace string) bool {
	return len(namespace) <= MaxNamespaceLength && namespacePattern.MatchString(namespace)
}

// ValidKey 判断配置键是否为小写点分形式。
func ValidKey(key string) bool {
	return len(key) <= MaxKeyLength && keyPattern.MatchString(key)
}

// ValidSHA256 判断字符串是否为 64 位小写十六进制摘要。
func ValidSHA256(sum string) bool {
	return sha256Pattern.MatchString(sum)
}

// ValueType 是配置值类型；反射到业务侧的解码方式。
type ValueType string

// 支持的值类型。
const (
	ValueTypeString   ValueType = "string"
	ValueTypeInt      ValueType = "int"
	ValueTypeBool     ValueType = "bool"
	ValueTypeDuration ValueType = "duration"
	ValueTypeJSON     ValueType = "json"
)

// Valid 判断值类型枚举合法性。
func (t ValueType) Valid() bool {
	switch t {
	case ValueTypeString, ValueTypeInt, ValueTypeBool, ValueTypeDuration, ValueTypeJSON:
		return true
	default:
		return false
	}
}

// ItemStatus 是配置键的治理状态。
type ItemStatus string

// 配置键治理状态枚举。
const (
	ItemStatusDraft      ItemStatus = "draft"
	ItemStatusActive     ItemStatus = "active"
	ItemStatusDeprecated ItemStatus = "deprecated"
	ItemStatusArchived   ItemStatus = "archived"
)

// Valid 判断状态枚举合法性。
func (s ItemStatus) Valid() bool {
	switch s {
	case ItemStatusDraft, ItemStatusActive, ItemStatusDeprecated, ItemStatusArchived:
		return true
	default:
		return false
	}
}

// itemTransitions 是配置键治理状态迁移矩阵。
//
// archived 是终态：恢复必须显式新建或走运维流程，避免归档被静默绕过。
var itemTransitions = map[ItemStatus][]ItemStatus{
	ItemStatusDraft:      {ItemStatusActive, ItemStatusArchived},
	ItemStatusActive:     {ItemStatusDeprecated, ItemStatusArchived},
	ItemStatusDeprecated: {ItemStatusActive, ItemStatusArchived},
	ItemStatusArchived:   {},
}

// CanTransitionTo 判断治理状态迁移是否合法。
func (s ItemStatus) CanTransitionTo(target ItemStatus) bool {
	for _, allowed := range itemTransitions[s] {
		if allowed == target {
			return true
		}
	}
	return false
}

// Item 是配置键元数据聚合根。
//
// CurrentVersion 指向最近一次写入的 config_item_version.version；
// Version 是元数据乐观锁，发布流程推进 current_version 时同步 +1。
type Item struct {
	ID             int64
	Env            string
	Namespace      string
	Key            string
	ValueType      ValueType
	Description    string
	Owner          string
	Status         ItemStatus
	CurrentVersion int64
	Version        int64
	Sensitive      bool
	ValidationJSON string
	ExpiresAt      *time.Time
	CreatedBy      string
	UpdatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Validate 校验配置键元数据的不变量。
func (i Item) Validate() error {
	if i.ID <= 0 {
		return fmt.Errorf("configcenter: item id is required")
	}
	if !ValidEnv(i.Env) {
		return fmt.Errorf("configcenter: invalid env %q", i.Env)
	}
	if !ValidNamespace(i.Namespace) {
		return fmt.Errorf("configcenter: invalid namespace %q", i.Namespace)
	}
	if !ValidKey(i.Key) {
		return fmt.Errorf("configcenter: invalid key %q", i.Key)
	}
	if !i.ValueType.Valid() {
		return fmt.Errorf("configcenter: invalid value_type %q", i.ValueType)
	}
	if len(i.Description) > MaxDescriptionLength {
		return fmt.Errorf("configcenter: description exceeds %d bytes", MaxDescriptionLength)
	}
	if strings.TrimSpace(i.Owner) == "" {
		return fmt.Errorf("configcenter: owner is required")
	}
	if len(i.Owner) > MaxOwnerLength {
		return fmt.Errorf("configcenter: owner exceeds %d bytes", MaxOwnerLength)
	}
	if !i.Status.Valid() {
		return fmt.Errorf("configcenter: invalid item status %q", i.Status)
	}
	if i.CurrentVersion < 0 {
		return fmt.Errorf("configcenter: current_version must be >= 0")
	}
	if i.Version < 1 {
		return fmt.Errorf("configcenter: version must be >= 1")
	}
	if err := validateJSON(i.ValidationJSON); err != nil {
		return fmt.Errorf("configcenter: validation_json: %w", err)
	}
	if strings.TrimSpace(i.CreatedBy) == "" || strings.TrimSpace(i.UpdatedBy) == "" {
		return fmt.Errorf("configcenter: created_by and updated_by are required")
	}
	if i.CreatedAt.IsZero() || i.UpdatedAt.IsZero() {
		return fmt.Errorf("configcenter: created_at and updated_at are required")
	}
	return nil
}

// validateJSON 校验可选 JSON 列；空串视为 NULL。
func validateJSON(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return jsonValid([]byte(raw))
}
