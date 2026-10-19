package storage

import (
	"context"
	"fmt"
	"strings"
)

// LifecycleRule 桶级对象生命周期规则（过期 / 中止未完成分片）。
// Prefix 为空表示整桶；非空时须为合法键前缀（不含前导 /）。
type LifecycleRule struct {
	ID                             string `yaml:"id" json:"id"`
	Prefix                         string `yaml:"prefix" json:"prefix"`
	ExpireDays                     int    `yaml:"expire_days" json:"expire_days"`
	AbortIncompleteMultipartDays   int    `yaml:"abort_incomplete_multipart_days" json:"abort_incomplete_multipart_days"`
}

// LifecycleConfig 生命周期装配；默认不写桶（须显式 ApplyOnStart）。
type LifecycleConfig struct {
	// ApplyOnStart 为 true 时启动调用 PutLifecycle（会改桶策略，生产慎用）。
	ApplyOnStart bool            `yaml:"apply_on_start"`
	Rules        []LifecycleRule `yaml:"rules"`
}

// LifecycleManager 桶生命周期管理；Memory/Local 不实现。
type LifecycleManager interface {
	PutLifecycle(ctx context.Context, rules []LifecycleRule) error
	GetLifecycle(ctx context.Context) ([]LifecycleRule, error)
}

// AsLifecycle 若 Store 支持生命周期则返回（含 PrefixStore 穿透探测）。
func AsLifecycle(s Store) (LifecycleManager, bool) {
	if s == nil {
		return nil, false
	}
	if p, ok := s.(*PrefixStore); ok {
		if _, ok := AsLifecycle(p.inner); !ok {
			return nil, false
		}
		return p, true
	}
	lm, ok := s.(LifecycleManager)
	return lm, ok
}

// ValidateLifecycleRules 校验规则列表；允许空列表（表示清空策略）。
func ValidateLifecycleRules(rules []LifecycleRule) error {
	seen := make(map[string]struct{}, len(rules))
	for i, r := range rules {
		id := strings.TrimSpace(r.ID)
		if id == "" {
			return fmt.Errorf("%w: lifecycle.rules[%d].id is required", ErrInvalidConfig, i)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("%w: duplicate lifecycle rule id %q", ErrInvalidConfig, id)
		}
		seen[id] = struct{}{}
		if strings.HasPrefix(r.Prefix, "/") {
			return fmt.Errorf("%w: lifecycle rule %q prefix must not start with /", ErrInvalidConfig, id)
		}
		prefix := strings.Trim(r.Prefix, "/")
		if prefix != "" {
			if err := ValidateKey(prefix); err != nil {
				return fmt.Errorf("%w: lifecycle rule %q prefix: %v", ErrInvalidConfig, id, err)
			}
		}
		if r.ExpireDays < 0 || r.AbortIncompleteMultipartDays < 0 {
			return fmt.Errorf("%w: lifecycle rule %q days must be >= 0", ErrInvalidConfig, id)
		}
		if r.ExpireDays == 0 && r.AbortIncompleteMultipartDays == 0 {
			return fmt.Errorf("%w: lifecycle rule %q needs expire_days or abort_incomplete_multipart_days", ErrInvalidConfig, id)
		}
	}
	return nil
}

// ApplyLifecycle 若 Store 支持则 PutLifecycle；不支持返回 ErrUnsupported。
func ApplyLifecycle(ctx context.Context, s Store, rules []LifecycleRule) error {
	if err := ValidateLifecycleRules(rules); err != nil {
		return err
	}
	lm, ok := AsLifecycle(s)
	if !ok {
		return fmt.Errorf("%w: lifecycle", ErrUnsupported)
	}
	return lm.PutLifecycle(ctx, rules)
}
