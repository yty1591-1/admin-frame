package domain

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// KeySpec 是代码侧声明的配置键契约（CFG-07：禁止硬编码，键必须声明
// owner、类型、默认值、校验规则与 reloadable）。
//
// 声明是唯一入口：管理面创建配置键、首次启动播种默认值（CC-F-44）与
// 双读校验（CC-F-42）都以 KeySpec 为基准；未声明的键拒绝写入。
type KeySpec struct {
	// Namespace 是配置命名空间，与 config_item.namespace 对齐。
	Namespace string
	// Key 是配置键（小写点分），与 config_item.key 对齐。
	Key string
	// ValueType 决定业务侧解码方式与默认值校验方式。
	ValueType ValueType
	// Default 是代码默认值；敏感键必须为空（密钥值不得出现在代码与 M1 表族）。
	Default string
	// Description 是用途说明，写入 config_item.description。
	Description string
	// Owner 是负责人或团队，写入 config_item.owner。
	Owner string
	// Reloadable 表示是否支持热更；false 表示启动期读取，变更需重启。
	Reloadable bool
	// Sensitive 表示敏感项；M1 仅允许登记，不允许写入值（走 config_secret，M2）。
	Sensitive bool
	// Required 表示必须由配置中心提供；与 Default 互斥（有默认值就不算必填）。
	Required bool
	// Enum 是 string 类型的允许值集合；空表示不限制。
	Enum []string
	// MinInt / MaxInt 是 int 类型的闭区间上下界；nil 表示不限制。
	MinInt *int64
	MaxInt *int64
	// Pattern 是 string 类型的正则约束；空表示不限制。
	Pattern string
	// Deprecated 标记键已废弃；仅用于治理展示，不阻断读取。
	Deprecated bool
	// SecretRule 是敏感键生成规则；仅 Sensitive=true 时有意义（M2）。
	SecretRule *SecretRule
}

// Validate 校验声明本身的一致性；装配期（Registry.Register）失败即拒绝启动。
func (s KeySpec) Validate() error {
	if !ValidNamespace(s.Namespace) {
		return fmt.Errorf("configcenter: invalid key spec namespace %q", s.Namespace)
	}
	if !ValidKey(s.Key) {
		return fmt.Errorf("configcenter: invalid key spec key %q", s.Key)
	}
	if !s.ValueType.Valid() {
		return fmt.Errorf("configcenter: key spec %s has invalid value_type %q", s.Key, s.ValueType)
	}
	if strings.TrimSpace(s.Description) == "" || len(s.Description) > MaxDescriptionLength {
		return fmt.Errorf("configcenter: key spec %s description is required and must not exceed %d bytes", s.Key, MaxDescriptionLength)
	}
	if strings.TrimSpace(s.Owner) == "" || len(s.Owner) > MaxOwnerLength {
		return fmt.Errorf("configcenter: key spec %s owner is required and must not exceed %d bytes", s.Key, MaxOwnerLength)
	}
	if s.Sensitive && s.Default != "" {
		return fmt.Errorf("configcenter: key spec %s is sensitive and must not carry a default value", s.Key)
	}
	if s.SecretRule != nil {
		if !s.Sensitive {
			return fmt.Errorf("configcenter: key spec %s secret_rule requires sensitive=true", s.Key)
		}
		if err := s.SecretRule.Validate(); err != nil {
			return fmt.Errorf("configcenter: key spec %s: %w", s.Key, err)
		}
	}
	if s.Required && s.Default != "" {
		return fmt.Errorf("configcenter: key spec %s cannot be required and carry a default value", s.Key)
	}
	if len(s.Enum) > 0 {
		if s.ValueType != ValueTypeString {
			return fmt.Errorf("configcenter: key spec %s enum is only supported for string values", s.Key)
		}
		seen := make(map[string]struct{}, len(s.Enum))
		for _, allowed := range s.Enum {
			if allowed == "" {
				return fmt.Errorf("configcenter: key spec %s enum must not contain empty values", s.Key)
			}
			if _, dup := seen[allowed]; dup {
				return fmt.Errorf("configcenter: key spec %s enum contains duplicate %q", s.Key, allowed)
			}
			seen[allowed] = struct{}{}
		}
	}
	if s.MinInt != nil || s.MaxInt != nil {
		if s.ValueType != ValueTypeInt {
			return fmt.Errorf("configcenter: key spec %s min/max is only supported for int values", s.Key)
		}
		if s.MinInt != nil && s.MaxInt != nil && *s.MinInt > *s.MaxInt {
			return fmt.Errorf("configcenter: key spec %s min must not exceed max", s.Key)
		}
	}
	if s.Pattern != "" {
		if s.ValueType != ValueTypeString {
			return fmt.Errorf("configcenter: key spec %s pattern is only supported for string values", s.Key)
		}
		if _, err := regexp.Compile(s.Pattern); err != nil {
			return fmt.Errorf("configcenter: key spec %s has invalid pattern: %w", s.Key, err)
		}
	}
	if s.Default != "" {
		if err := s.ValidateValue(s.Default); err != nil {
			return fmt.Errorf("configcenter: key spec %s default is invalid: %w", s.Key, err)
		}
	}
	return nil
}

// ValidateValue 按类型、枚举、区间与正则校验一个配置值。
//
// 空串表示「未设置」，仅跳过类型解析；Required 的缺失由 Registry.ValidateValues
// 判定。敏感键的值一律拒绝（M1 不允许承载密钥值）。
func (s KeySpec) ValidateValue(raw string) error {
	if s.Sensitive {
		return fmt.Errorf("configcenter: %s is sensitive; values must be stored via config_secret (M2)", s.Key)
	}
	if raw == "" {
		return nil
	}
	switch s.ValueType {
	case ValueTypeString:
		if len(s.Enum) > 0 && !contains(s.Enum, raw) {
			return fmt.Errorf("value %q is not in enum %v", raw, s.Enum)
		}
		if s.Pattern != "" {
			re, err := regexp.Compile(s.Pattern)
			if err != nil {
				return fmt.Errorf("invalid pattern: %w", err)
			}
			if !re.MatchString(raw) {
				return fmt.Errorf("value %q does not match pattern %q", raw, s.Pattern)
			}
		}
	case ValueTypeInt:
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("value %q is not an integer", raw)
		}
		if s.MinInt != nil && value < *s.MinInt {
			return fmt.Errorf("value %d is below min %d", value, *s.MinInt)
		}
		if s.MaxInt != nil && value > *s.MaxInt {
			return fmt.Errorf("value %d exceeds max %d", value, *s.MaxInt)
		}
	case ValueTypeBool:
		if _, err := strconv.ParseBool(raw); err != nil {
			return fmt.Errorf("value %q is not a bool", raw)
		}
	case ValueTypeDuration:
		if _, err := time.ParseDuration(raw); err != nil {
			return fmt.Errorf("value %q is not a duration", raw)
		}
	case ValueTypeJSON:
		if err := jsonValid([]byte(raw)); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported value_type %q", s.ValueType)
	}
	return nil
}

// ValidationJSON 把声明中的校验规则序列化为 config_item.validation_json。
//
// 无规则返回空串（落库为 NULL）；不包含默认值本身，避免重复存储。
func (s KeySpec) ValidationJSON() string {
	rules := make(map[string]any, 4)
	if len(s.Enum) > 0 {
		rules["enum"] = s.Enum
	}
	if s.MinInt != nil {
		rules["min"] = *s.MinInt
	}
	if s.MaxInt != nil {
		rules["max"] = *s.MaxInt
	}
	if s.Pattern != "" {
		rules["pattern"] = s.Pattern
	}
	if len(rules) == 0 {
		return ""
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		return ""
	}
	return string(raw)
}

// contains 判断枚举是否包含目标值。
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
