// Package domain 是 featureflag 域模型：特性开关（ENT-11）。
//
// 本包只承载纯类型、状态机与校验，不涉缓存 / SQL / HTTP：
// 管理用例在 app 包（FlagService），评估在 app.EvaluationService，
// 消费方通过自定义端口接口注入（业务域不 import 本包内部）。
package domain

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Key 正则：module.feature 小写点分（feature-flag-plan.md §2.A）。
var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// 长度上限（与迁移 000020 列宽对齐）。
const (
	MaxKeyLength         = 128
	MaxDescriptionLength = 512
	MaxRules             = 32
)

// FlagStatus 是 flag 的治理状态：enabled（启用）/ disabled（禁用）/
// expired（到期自动禁用，等价 disabled 但留痕可辨）。
type FlagStatus string

// flag 治理状态枚举。
const (
	StatusEnabled  FlagStatus = "enabled"
	StatusDisabled FlagStatus = "disabled"
	StatusExpired  FlagStatus = "expired"
)

// Valid 判断状态枚举合法性。
func (s FlagStatus) Valid() bool {
	switch s {
	case StatusEnabled, StatusDisabled, StatusExpired:
		return true
	default:
		return false
	}
}

var statusTransitions = map[FlagStatus][]FlagStatus{
	StatusEnabled:  {StatusDisabled, StatusExpired},
	StatusDisabled: {StatusEnabled},
	// expired 是治理终态：恢复必须走显式编辑（改 expires_at / permanent），
	// 不允许一键复活——防止到期治理被静默绕过。
	StatusExpired: {},
}

// CanTransitionTo 判断治理状态迁移合法性。
func (s FlagStatus) CanTransitionTo(target FlagStatus) bool {
	for _, allowed := range statusTransitions[s] {
		if allowed == target {
			return true
		}
	}
	return false
}

// Transitions 返回状态迁移矩阵快照（副本），供测试快照与门禁消费。
func Transitions() map[FlagStatus][]FlagStatus {
	out := make(map[FlagStatus][]FlagStatus, len(statusTransitions))
	for from, targets := range statusTransitions {
		copied := make([]FlagStatus, len(targets))
		copy(copied, targets)
		out[from] = copied
	}
	return out
}

// Flag 是特性开关聚合根。
//
// 不变量：
//   - key 全局唯一（uk 兜底 + ErrDuplicateKey）；
//   - 非 permanent 必须带 expires_at（到期治理，§17.3）；
//   - permanent 必须无 expires_at（二者互斥，二选一语义）；
//   - 规则最多 MaxRules 条（防御配置爆炸）。
type Flag struct {
	Key            string
	Description    string
	Status         FlagStatus
	DefaultEnabled bool
	Rules          []Rule
	Permanent      bool
	ExpiresAt      time.Time // 零值 = 无到期（仅 permanent 合法）
	Version        int64
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewFlag 构造新 flag：防御性默认（default_enabled=false、规则可空由
// 调用方显式传入但 API 层拒绝 default_enabled=true 的裸创建——防御
// 校验在 app 层，领域层只保证结构合法）。
func NewFlag(key, description string, rules []Rule, permanent bool, expiresAt time.Time,
	createdBy string, now time.Time) (Flag, error) {
	trimmedKey := strings.TrimSpace(key)
	switch {
	case trimmedKey == "":
		return Flag{}, ErrKeyRequired
	case len(trimmedKey) > MaxKeyLength:
		return Flag{}, ErrKeyTooLong
	case !keyPattern.MatchString(trimmedKey):
		return Flag{}, ErrKeyInvalid
	}
	if l := len([]rune(strings.TrimSpace(description))); l > MaxDescriptionLength {
		return Flag{}, ErrDescriptionTooLong
	}
	if len(rules) > MaxRules {
		return Flag{}, ErrTooManyRules
	}
	for i := range rules {
		if err := rules[i].Validate(); err != nil {
			return Flag{}, fmt.Errorf("rule[%d]: %w", i, err)
		}
	}
	// permanent / expires_at 二选一。
	switch {
	case permanent && !expiresAt.IsZero():
		return Flag{}, ErrPermanentWithExpiry
	case !permanent && expiresAt.IsZero():
		return Flag{}, ErrExpiryRequired
	case !permanent && !expiresAt.After(now):
		return Flag{}, ErrExpiryInPast
	}
	trimmedBy := strings.TrimSpace(createdBy)
	if trimmedBy == "" {
		return Flag{}, ErrCreatedByRequired
	}
	return Flag{
		Key:         trimmedKey,
		Description: strings.TrimSpace(description),
		Status:      StatusEnabled,
		Rules:       append([]Rule(nil), rules...),
		Permanent:   permanent,
		ExpiresAt:   expiresAt,
		Version:     1,
		CreatedBy:   trimmedBy,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// Validate 聚合不变量校验（仓储读出后自验 + 更新前防漂移）。
func (f Flag) Validate() error {
	if !keyPattern.MatchString(f.Key) {
		return ErrKeyInvalid
	}
	if !f.Status.Valid() {
		return ErrStatusInvalid
	}
	if len(f.Rules) > MaxRules {
		return ErrTooManyRules
	}
	for i := range f.Rules {
		if err := f.Rules[i].Validate(); err != nil {
			return fmt.Errorf("rule[%d]: %w", i, err)
		}
	}
	switch {
	case f.Permanent && !f.ExpiresAt.IsZero():
		return ErrPermanentWithExpiry
	case !f.Permanent && f.ExpiresAt.IsZero():
		return ErrExpiryRequired
	}
	if f.Version < 1 {
		return ErrVersionInvalid
	}
	return nil
}

// Transition 治理状态迁移（乐观锁由仓储承担，本方法只做状态机守卫）。
// 返回迁移后的副本；非法迁移返回 ErrInvalidTransition。
func (f Flag) Transition(target FlagStatus, now time.Time) (Flag, error) {
	if !f.Status.CanTransitionTo(target) {
		return Flag{}, ErrInvalidTransition
	}
	out := f
	out.Status = target
	out.UpdatedAt = now
	return out, nil
}

// MarshalRules 序列化规则数组（rules_json 列）。
func (f Flag) MarshalRules() ([]byte, error) {
	if f.Rules == nil {
		f.Rules = []Rule{}
	}
	return json.Marshal(f.Rules)
}

// UnmarshalRules 反序列化规则数组并校验（防脏数据漂移）。
func (f *Flag) UnmarshalRules(raw []byte) error {
	var rules []Rule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return fmt.Errorf("featureflag: decode rules: %w", err)
	}
	for i := range rules {
		if err := rules[i].Validate(); err != nil {
			return fmt.Errorf("rule[%d]: %w", i, err)
		}
	}
	f.Rules = rules
	return nil
}
