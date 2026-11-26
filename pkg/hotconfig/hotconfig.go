// Package hotconfig 提供配置热更新所需的不可变视图与原子替换。
//
// 设计约束见 docs/adr/0015-config-hot-reload-whitelist.md：
//   - 只有列入白名单的键进入视图，其余键即使发布也不在运行期生效；
//   - 视图一旦构建完成即不可变，读方每次读当前指针，不做逐键加锁；
//   - 构建失败（类型 / 枚举 / 解析错误）整体拒绝，保留上一份视图；
//   - 取值完全相同则不替换、不推进版本。
//
// 本包不依赖 internal，供 pkg 与 cmd 双方使用（LAYER-10）。
package hotconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// ValueType 是热更键的取值类型。
type ValueType string

const (
	// TypeString 字符串。
	TypeString ValueType = "string"
	// TypeInt 整数。
	TypeInt ValueType = "int"
	// TypeBool 布尔（true/false/1/0）。
	TypeBool ValueType = "bool"
	// TypeDuration Go duration 文本（如 5s、1m30s）。
	TypeDuration ValueType = "duration"
)

// Spec 是热更视图需要的最小键声明。
//
// 只保留构建视图必需的字段；owner、描述、默认值等治理信息仍归
// internal/configcenter/spec，不进入运行期结构。
type Spec struct {
	// Key 是配置键（小写点分）。
	Key string
	// Type 是取值类型；决定校验与读取方式。
	Type ValueType
	// Enum 非空时，取值必须命中其中之一。
	Enum []string
	// Pattern 非空时，字符串取值必须匹配（与声明期正则一致）。
	Pattern *regexp.Regexp
	// Min / Max 是整数取值的闭区间边界；nil 表示不限制。
	Min *int64
	Max *int64
}

// View 是构建完成后不可变的热更视图。
type View struct {
	values      map[string]string
	fingerprint string
	version     int64
	releaseID   int64
	builtAt     time.Time
}

// Build 校验并构建视图。
//
// values 只应包含白名单键；未在 specs 中声明的键会被忽略（防止把密钥或
// 启动期配置带进热更视图）。任一已声明键解析失败都会整体返回错误，
// 由调用方保留上一份视图。
func Build(specs []Spec, values map[string]string, version, releaseID int64,
	now time.Time) (*View, error) {
	byKey := make(map[string]Spec, len(specs))
	for _, spec := range specs {
		if err := validateSpec(spec); err != nil {
			return nil, err
		}
		byKey[spec.Key] = spec
	}
	view := &View{
		values:    make(map[string]string, len(values)),
		version:   version,
		releaseID: releaseID,
		builtAt:   now,
	}
	for key, raw := range values {
		spec, ok := byKey[key]
		if !ok {
			continue
		}
		normalized, err := normalize(spec, raw)
		if err != nil {
			return nil, err
		}
		view.values[key] = normalized
	}
	view.fingerprint = fingerprint(view.values)
	return view, nil
}

// Fingerprint 返回取值指纹；取值相同则指纹相同。
func (v *View) Fingerprint() string {
	if v == nil {
		return ""
	}
	return v.fingerprint
}

// Version 返回视图版本（调用方按需递增）。
func (v *View) Version() int64 {
	if v == nil {
		return 0
	}
	return v.version
}

// ReleaseID 返回视图对应的发布 ID；0 表示启动期初始视图。
func (v *View) ReleaseID() int64 {
	if v == nil {
		return 0
	}
	return v.releaseID
}

// BuiltAt 返回视图构建时间。
func (v *View) BuiltAt() time.Time {
	if v == nil {
		return time.Time{}
	}
	return v.builtAt
}

// Keys 返回视图内的键（已排序），供日志与指标使用；不返回值。
func (v *View) Keys() []string {
	if v == nil || len(v.values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(v.values))
	for key := range v.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Lookup 返回原始文本值与是否存在。
func (v *View) Lookup(key string) (string, bool) {
	if v == nil {
		return "", false
	}
	value, ok := v.values[key]
	return value, ok
}

// String 读取字符串键；缺失或类型不符返回 defaultValue。
func (v *View) String(key, defaultValue string) string {
	raw, ok := v.Lookup(key)
	if !ok {
		return defaultValue
	}
	return raw
}

// Int 读取整数键；缺失返回 defaultValue。
//
// 构建期已校验格式，这里不再报错，避免读路径分支。
func (v *View) Int(key string, defaultValue int64) int64 {
	raw, ok := v.Lookup(key)
	if !ok {
		return defaultValue
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return defaultValue
	}
	return parsed
}

// Duration 读取时长键；缺失或解析失败返回 defaultValue。
func (v *View) Duration(key string, defaultValue time.Duration) time.Duration {
	raw, ok := v.Lookup(key)
	if !ok {
		return defaultValue
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return defaultValue
	}
	return parsed
}

// Bool 读取布尔键；缺失返回 defaultValue。
func (v *View) Bool(key string, defaultValue bool) bool {
	raw, ok := v.Lookup(key)
	if !ok {
		return defaultValue
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return defaultValue
	}
	return parsed
}

// Holder 持有当前视图，提供无锁读取与原子替换。
type Holder struct {
	current atomic.Pointer[View]
}

// NewHolder 构造 Holder；initial 为 nil 时退化为空视图。
func NewHolder(initial *View) *Holder {
	holder := &Holder{}
	if initial == nil {
		initial = &View{values: map[string]string{}, fingerprint: fingerprint(nil)}
	}
	holder.current.Store(initial)
	return holder
}

// Current 返回当前视图；永不为 nil。
func (h *Holder) Current() *View {
	if h == nil {
		return nil
	}
	if view := h.current.Load(); view != nil {
		return view
	}
	return &View{values: map[string]string{}}
}

// Store 在指纹变化时替换视图。
//
// 返回是否实际替换：取值完全一致时不替换，避免无谓唤醒与版本抖动。
func (h *Holder) Store(next *View) bool {
	if h == nil || next == nil {
		return false
	}
	for {
		previous := h.current.Load()
		if previous != nil && previous.Fingerprint() == next.Fingerprint() {
			return false
		}
		if h.current.CompareAndSwap(previous, next) {
			return true
		}
	}
}

// validateSpec 校验声明本身合法。
func validateSpec(spec Spec) error {
	if strings.TrimSpace(spec.Key) == "" {
		return fmt.Errorf("hotconfig: key is required")
	}
	switch spec.Type {
	case TypeString, TypeInt, TypeBool, TypeDuration:
	default:
		return fmt.Errorf("hotconfig: key %s has unsupported type %q", spec.Key, spec.Type)
	}
	return nil
}

// normalize 按类型校验并规范化取值。
func normalize(spec Spec, raw string) (string, error) {
	switch spec.Type {
	case TypeString:
		if len(spec.Enum) > 0 && !contains(spec.Enum, raw) {
			return "", fmt.Errorf("hotconfig: key %s value %q is not in enum", spec.Key, raw)
		}
		if spec.Pattern != nil && !spec.Pattern.MatchString(raw) {
			return "", fmt.Errorf("hotconfig: key %s value %q does not match pattern", spec.Key, raw)
		}
		return raw, nil
	case TypeInt:
		parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return "", fmt.Errorf("hotconfig: key %s is not an integer: %w", spec.Key, err)
		}
		if spec.Min != nil && parsed < *spec.Min {
			return "", fmt.Errorf("hotconfig: key %s value %d is below minimum %d", spec.Key, parsed, *spec.Min)
		}
		if spec.Max != nil && parsed > *spec.Max {
			return "", fmt.Errorf("hotconfig: key %s value %d is above maximum %d", spec.Key, parsed, *spec.Max)
		}
		return strconv.FormatInt(parsed, 10), nil
	case TypeBool:
		parsed, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return "", fmt.Errorf("hotconfig: key %s is not a boolean: %w", spec.Key, err)
		}
		return strconv.FormatBool(parsed), nil
	case TypeDuration:
		parsed, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return "", fmt.Errorf("hotconfig: key %s is not a duration: %w", spec.Key, err)
		}
		return parsed.String(), nil
	default:
		return "", fmt.Errorf("hotconfig: key %s has unsupported type %q", spec.Key, spec.Type)
	}
}

// contains 判断枚举命中。
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// fingerprint 计算取值指纹（键排序后拼接，不含版本号）。
func fingerprint(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		hash.Write([]byte(key))
		hash.Write([]byte{'='})
		hash.Write([]byte(values[key]))
		hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}
