package spec

import (
	"fmt"
	"regexp"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/pkg/hotconfig"
)

// HotKeys 是 ADR-0015 附录 A 的首批热更白名单。
//
// 白名单是运行期契约：只有这里的键会进入热更视图，其余键即使 `Reloadable=true`
// 也只走「发布 + 重启」。新增或移除键必须同步 ADR-0015 与本切片。
var HotKeys = []string{
	"log.level",
	"idempotency.ttl",
	"rate_limit.rps",
	"rate_limit.burst",
	"rate_limit.idle_ttl",
	"rate_limit.degrade_on_failure",
	"async.outbox.poll_interval",
	"async.outbox.batch_size",
	"async.outbox.lease",
	"async.outbox.max_attempts",
	"async.consumer.visibility_timeout",
	"async.consumer.retry_backoff",
}

// HotSpecs 返回白名单键的热更声明（键 / 类型 / 枚举）。
//
// 任一白名单键未在注册表声明、或声明未标 `Reloadable`、或类型不受支持，
// 都视为装配期错误：白名单与键声明必须一致，不允许静默降级。
func HotSpecs() ([]hotconfig.Spec, error) {
	registry, err := Platform()
	if err != nil {
		return nil, err
	}
	specs := make([]hotconfig.Spec, 0, len(HotKeys))
	for _, key := range HotKeys {
		keySpec, ok := lookupAcrossNamespaces(registry, key)
		if !ok {
			return nil, fmt.Errorf("configcenter: hot key %s has no declaration", key)
		}
		if !keySpec.Reloadable {
			return nil, fmt.Errorf("configcenter: hot key %s must be declared Reloadable", key)
		}
		valueType, err := hotValueType(keySpec.ValueType)
		if err != nil {
			return nil, fmt.Errorf("configcenter: hot key %s: %w", key, err)
		}
		pattern, err := compilePattern(keySpec)
		if err != nil {
			return nil, fmt.Errorf("configcenter: hot key %s: %w", key, err)
		}
		specs = append(specs, hotconfig.Spec{
			Key:     keySpec.Key,
			Type:    valueType,
			Enum:    keySpec.Enum,
			Pattern: pattern,
			Min:     keySpec.MinInt,
			Max:     keySpec.MaxInt,
		})
	}
	return specs, nil
}

// HotKeySet 返回白名单集合，供运行期快速判断某个键是否可热更。
func HotKeySet() map[string]struct{} {
	set := make(map[string]struct{}, len(HotKeys))
	for _, key := range HotKeys {
		set[key] = struct{}{}
	}
	return set
}

// lookupAcrossNamespaces 在所有 namespace 中查找键声明。
//
// 键名本身已包含域前缀（如 `rate_limit.rps`），查找按 key 精确匹配。
func lookupAcrossNamespaces(registry *Registry, key string) (domain.KeySpec, bool) {
	for _, namespace := range registry.Namespaces() {
		if keySpec, ok := registry.Lookup(namespace, key); ok {
			return keySpec, true
		}
	}
	return domain.KeySpec{}, false
}

// compilePattern 预编译声明期正则；空模式返回 nil。
func compilePattern(keySpec domain.KeySpec) (*regexp.Regexp, error) {
	if keySpec.Pattern == "" {
		return nil, nil
	}
	compiled, err := regexp.Compile(keySpec.Pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern %q: %w", keySpec.Pattern, err)
	}
	return compiled, nil
}

// hotValueType 把领域类型映射为热更视图支持的类型。
func hotValueType(valueType domain.ValueType) (hotconfig.ValueType, error) {
	switch valueType {
	case domain.ValueTypeString:
		return hotconfig.TypeString, nil
	case domain.ValueTypeInt:
		return hotconfig.TypeInt, nil
	case domain.ValueTypeBool:
		return hotconfig.TypeBool, nil
	case domain.ValueTypeDuration:
		return hotconfig.TypeDuration, nil
	default:
		// JSON 等结构化值不做热更：无法在构建期做廉价校验，且消费方通常需要重新装配。
		return "", fmt.Errorf("value type %q is not supported for hot reload", valueType)
	}
}

// DeclaredKeySet 返回全部已声明键，用于区分「已声明但不可热更」的变更。
func DeclaredKeySet() (map[string]struct{}, error) {
	registry, err := Platform()
	if err != nil {
		return nil, err
	}
	declared := make(map[string]struct{}, 128)
	for _, namespace := range registry.Namespaces() {
		for _, keySpec := range registry.List(namespace) {
			declared[keySpec.Key] = struct{}{}
		}
	}
	return declared, nil
}
