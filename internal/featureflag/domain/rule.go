package domain

import (
	"errors"
	"fmt"
	"strings"
)

// 错误哨兵：transport / app 以 errors.Is 识别后翻译业务码。
var (
	ErrKeyRequired         = errors.New("featureflag: key is required")
	ErrKeyTooLong          = errors.New("featureflag: key too long")
	ErrKeyInvalid          = errors.New("featureflag: key must be module.feature lowercase dotted")
	ErrDescriptionTooLong  = errors.New("featureflag: description too long")
	ErrTooManyRules        = errors.New("featureflag: too many rules")
	ErrPermanentWithExpiry = errors.New("featureflag: permanent flag must not carry expiry")
	ErrExpiryRequired      = errors.New("featureflag: non-permanent flag requires expires_at")
	ErrExpiryInPast        = errors.New("featureflag: expires_at must be in the future")
	ErrCreatedByRequired   = errors.New("featureflag: created_by is required")
	ErrVersionInvalid      = errors.New("featureflag: version must be positive")
	ErrStatusInvalid       = errors.New("featureflag: invalid status")
	ErrInvalidTransition   = errors.New("featureflag: invalid status transition")
	ErrDuplicateKey        = errors.New("featureflag: duplicate flag key")
	ErrNotFound            = errors.New("featureflag: flag not found")
	ErrVersionConflict     = errors.New("featureflag: version conflict")
)

// 评估维度与灰度常量。
const (
	// MaxPercentage 是百分比上限（万分比：10000 = 100%）。
	MaxPercentage = 10000
	// ChannelAdmin / ChannelBiz / ChannelC 是评估渠道枚举，
	// 字面值与 auth.Principal.Channel 保持一致。
	ChannelAdmin = "admin"
	ChannelBiz   = "biz"
	ChannelC     = "c"
)

// Rule 是一条灰度规则：三种维度互斥（单规则只表达一种匹配），
// flag 的 rules 数组按序评估，首个命中生效。
type Rule struct {
	// Channel 端别匹配（admin/biz/c）；空 = 不限渠道。
	Channel string `json:"channel,omitempty"`
	// TenantIDs 显式租户白名单；不接受通配符（全量放开走 percentage
	// 10000 或 default_enabled，防误操作）。空 = 不限租户。
	TenantIDs []string `json:"tenant_ids,omitempty"`
	// Percentage 万分比灰度（0–10000）；按 (flag_key, subject_id)
	// 稳定哈希分桶。0 = 恒不命中（合法但无意义，允许保留占位）。
	Percentage int `json:"percentage,omitempty"`
}

// Kind 返回规则类型（诊断与测试用）：channel / tenant / percentage / empty。
func (r Rule) Kind() string {
	switch {
	case r.Channel != "":
		return "channel"
	case len(r.TenantIDs) > 0:
		return "tenant"
	case r.Percentage > 0:
		return "percentage"
	default:
		return "empty"
	}
}

// Validate 规则不变量：三维度互斥、值域合法。
func (r Rule) Validate() error {
	dimensions := 0
	if strings.TrimSpace(r.Channel) != "" {
		dimensions++
	}
	if len(r.TenantIDs) > 0 {
		dimensions++
	}
	if r.Percentage != 0 {
		dimensions++
	}
	if dimensions > 1 {
		return fmt.Errorf("featureflag: rule must express exactly one dimension (channel / tenant_ids / percentage)")
	}
	if channel := strings.TrimSpace(r.Channel); channel != "" &&
		channel != ChannelAdmin && channel != ChannelBiz && channel != ChannelC {
		return fmt.Errorf("featureflag: rule channel must be admin|biz|c, got %q", channel)
	}
	for _, id := range r.TenantIDs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("featureflag: rule tenant_ids must not contain empty entries")
		}
	}
	if r.Percentage < 0 || r.Percentage > MaxPercentage {
		return fmt.Errorf("featureflag: rule percentage must be within [0,%d]", MaxPercentage)
	}
	return nil
}

// Matches 判断规则是否命中评估上下文；percentage 维度由评估引擎的
// 稳定哈希承担（本方法不做哈希——分桶逻辑在 infrastructure/hash.go，
// 便于替换与属性测试）。
func (r Rule) Matches(channel, tenantID string) (bool, error) {
	if err := r.Validate(); err != nil {
		return false, err
	}
	if r.Channel != "" && r.Channel != channel {
		return false, nil
	}
	if len(r.TenantIDs) > 0 {
		for _, id := range r.TenantIDs {
			if id == tenantID {
				return true, nil
			}
		}
		return false, nil
	}
	return true, nil
}
