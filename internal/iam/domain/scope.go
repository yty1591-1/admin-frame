package domain

import (
	"regexp"
	"slices"
	"strings"
)

// Channel 是 IAM 服务覆盖的端别。
//
// 与 catalog.Channel 的区别：catalog 允许 both（描述「哪端可授予」），
// 角色与绑定必须落到具体一端，因此这里只有 admin / biz。
type Channel string

const (
	// ChannelAdmin 运营后台（A 端）。
	ChannelAdmin Channel = "admin"
	// ChannelBiz 商家端（B 端）。
	ChannelBiz Channel = "biz"
)

// Validate 校验端别取值。
func (c Channel) Validate() error {
	switch c {
	case ChannelAdmin, ChannelBiz:
		return nil
	default:
		return errInvalid(ErrInvalidChannel, "channel %q must be admin or biz", string(c))
	}
}

// String 返回端别字面量。
func (c Channel) String() string { return string(c) }

// RoleScopeType 是角色的作用域：A 端全局或 B 端单商户。
type RoleScopeType string

const (
	// RoleScopePlatform A 端平台级角色，不归属任何商户。
	RoleScopePlatform RoleScopeType = "platform"
	// RoleScopeMerchant B 端商户内角色，必须归属一个商户。
	RoleScopeMerchant RoleScopeType = "merchant"
)

// Validate 校验作用域取值。
func (s RoleScopeType) Validate() error {
	switch s {
	case RoleScopePlatform, RoleScopeMerchant:
		return nil
	default:
		return errInvalid(ErrInvalidRoleScope, "role scope %q must be platform or merchant", string(s))
	}
}

// RoleType 是角色类型，决定不可变的前置校验与破玻璃行为。
type RoleType string

const (
	// RoleTypeSystem 迁移种子写入的系统内置角色。
	RoleTypeSystem RoleType = "system"
	// RoleTypeCustom 运营 / 商户自建角色。
	RoleTypeCustom RoleType = "custom"
	// RoleTypeBreakglass 破玻璃角色；仅审批通过后限时绑定，运行时展开 *。
	RoleTypeBreakglass RoleType = "breakglass"
)

// Validate 校验角色类型取值。
func (t RoleType) Validate() error {
	switch t {
	case RoleTypeSystem, RoleTypeCustom, RoleTypeBreakglass:
		return nil
	default:
		return errInvalid(ErrInvalidRoleScope, "role type %q must be system, custom or breakglass", string(t))
	}
}

// RoleStatus 是角色生命周期状态。
type RoleStatus string

const (
	// RoleStatusActive 生效；快照加载其权限。
	RoleStatusActive RoleStatus = "active"
	// RoleStatusDisabled 停用；保留数据但不再进入快照。
	RoleStatusDisabled RoleStatus = "disabled"
)

// Validate 校验状态取值。
func (s RoleStatus) Validate() error {
	switch s {
	case RoleStatusActive, RoleStatusDisabled:
		return nil
	default:
		return errInvalid(ErrRoleNotActive, "role status %q must be active or disabled", string(s))
	}
}

// SubjectType 是被授权主体类型，必须与端别一致。
type SubjectType string

const (
	// SubjectStaff A 端员工。
	SubjectStaff SubjectType = "staff"
	// SubjectMerchantStaff B 端商户员工。
	SubjectMerchantStaff SubjectType = "merchant_staff"
)

// Validate 校验主体类型取值。
func (t SubjectType) Validate() error {
	switch t {
	case SubjectStaff, SubjectMerchantStaff:
		return nil
	default:
		return errInvalid(ErrInvalidSubjectType, "subject type %q must be staff or merchant_staff", string(t))
	}
}

// DataScopeType 是数据范围类型，与功能权限正交。
type DataScopeType string

const (
	// DataScopeAll 全部数据；仅 A 端允许。
	DataScopeAll DataScopeType = "all"
	// DataScopeMerchant 当前商户数据；A / B 端均可。
	DataScopeMerchant DataScopeType = "merchant"
	// DataScopeSelf 本人数据；A / B 端均可。
	DataScopeSelf DataScopeType = "self"
	// DataScopeMerchantSet 指定商户集合；仅 A 端允许。
	DataScopeMerchantSet DataScopeType = "merchant_set"
)

const (
	// ResourceAll 表示数据范围覆盖全部资源。
	ResourceAll = "*"
	// MaxMerchantSetSize merchant_set 允许的最大商户数量（与 §4.4 一致）。
	MaxMerchantSetSize = 100
)

// breadth 返回范围宽窄排序值，用于多角色合并时取最宽。
func (t DataScopeType) breadth() int {
	switch t {
	case DataScopeAll:
		return 3
	case DataScopeMerchantSet:
		return 2
	case DataScopeMerchant:
		return 1
	case DataScopeSelf:
		return 0
	default:
		return -1
	}
}

// Validate 校验数据范围类型是否允许在给定端别使用。
//
// B 端只允许 merchant / self：B 端出现 all 或 merchant_set 即视为越权配置，
// 必须拒绝而不是在查询时静默收窄，否则错误配置会被掩盖到下一次审计。
func (t DataScopeType) Validate(channel Channel) error {
	switch t {
	case DataScopeAll, DataScopeMerchantSet:
		if channel == ChannelBiz {
			return errInvalid(ErrInvalidDataScope, "data scope %q is not allowed for channel biz", string(t))
		}
		return nil
	case DataScopeMerchant, DataScopeSelf:
		return nil
	default:
		return errInvalid(ErrInvalidDataScope,
			"data scope %q must be all, merchant, self or merchant_set", string(t))
	}
}

// scopeResourcePattern 约束资源段：* 或单个小写资源名（与权限码首段一致）。
var scopeResourcePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// DataScope 是角色对某类资源的数据可见范围值对象。
type DataScope struct {
	// Resource 资源段，"*" 或权限码首段（如 order / file）。
	Resource string
	// Type 范围类型。
	Type DataScopeType
	// MerchantIDs 仅 merchant_set 使用；其他类型必须为空。
	MerchantIDs []string
}

// NewDataScope 构造并校验数据范围（端别无关部分）。
//
// 端别限制需调用 Validate(channel)；构造时不知道角色端别，因此不在此处判断。
func NewDataScope(resource string, scopeType DataScopeType, merchantIDs []string) (DataScope, error) {
	scope := DataScope{
		Resource:    resource,
		Type:        scopeType,
		MerchantIDs: dedupeNonEmpty(merchantIDs),
	}
	if err := scope.validateStructural(); err != nil {
		return DataScope{}, err
	}
	return scope, nil
}

// Validate 在端别上下文中校验数据范围。
func (d DataScope) Validate(channel Channel) error {
	if err := channel.Validate(); err != nil {
		return err
	}
	if err := d.validateStructural(); err != nil {
		return err
	}
	return d.Type.Validate(channel)
}

// validateStructural 校验与端别无关的约束。
func (d DataScope) validateStructural() error {
	if d.Resource == "" {
		return errInvalid(ErrInvalidDataScope, "data scope resource is required")
	}
	if d.Resource != ResourceAll && !scopeResourcePattern.MatchString(d.Resource) {
		return errInvalid(ErrInvalidDataScope,
			"data scope resource %q must be \"*\" or a lowercase resource segment", d.Resource)
	}
	if d.Type.breadth() < 0 {
		return errInvalid(ErrInvalidDataScope,
			"data scope %q must be all, merchant, self or merchant_set", string(d.Type))
	}

	switch d.Type {
	case DataScopeMerchantSet:
		if len(d.MerchantIDs) == 0 {
			return errInvalid(ErrInvalidDataScope, "data scope merchant_set requires at least one merchant id")
		}
		if len(d.MerchantIDs) > MaxMerchantSetSize {
			return errInvalid(ErrMerchantSetTooLarge,
				"data scope merchant_set supports at most %d merchants, got %d", MaxMerchantSetSize, len(d.MerchantIDs))
		}
		for _, id := range d.MerchantIDs {
			if id == "" {
				return errInvalid(ErrInvalidDataScope, "data scope merchant_set contains empty merchant id")
			}
		}
	default:
		if len(d.MerchantIDs) > 0 {
			return errInvalid(ErrInvalidDataScope,
				"data scope %q must not carry merchant ids", string(d.Type))
		}
	}
	return nil
}

// Equal 判断两个数据范围是否等价（商户集合忽略顺序）。
func (d DataScope) Equal(other DataScope) bool {
	if d.Resource != other.Resource || d.Type != other.Type {
		return false
	}
	if len(d.MerchantIDs) != len(other.MerchantIDs) {
		return false
	}
	left := slices.Clone(d.MerchantIDs)
	right := slices.Clone(other.MerchantIDs)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}

// MergeDataScopes 按资源合并多角色的数据范围，取并集（最宽）。
//
// 规则（§4.4）：
//   - 同一资源任一角色为 all，结果为 all；
//   - merchant_set 与 merchant / self 合并时取 merchant_set，商户集合求并集；
//   - merchant 与 self 合并时取 merchant（更宽）；
//   - 合并结果按 Resource 排序，保证确定性。
//
// 返回错误表示输入非法或并集超过 merchant_set 上限；不得静默截断，
// 否则会出现「授权了但查不到」的隐性越权边界。
func MergeDataScopes(scopes []DataScope) ([]DataScope, error) {
	merged := make(map[string]DataScope, len(scopes))
	for _, scope := range scopes {
		if err := scope.validateStructural(); err != nil {
			return nil, err
		}
		current, ok := merged[scope.Resource]
		if !ok {
			scope.MerchantIDs = slices.Clone(scope.MerchantIDs)
			merged[scope.Resource] = scope
			continue
		}
		next, err := mergeTwo(current, scope)
		if err != nil {
			return nil, err
		}
		merged[scope.Resource] = next
	}

	out := make([]DataScope, 0, len(merged))
	for _, scope := range merged {
		out = append(out, scope)
	}
	slices.SortFunc(out, func(a, b DataScope) int { return strings.Compare(a.Resource, b.Resource) })
	return out, nil
}

// mergeTwo 合并同一资源的两个数据范围。
func mergeTwo(a, b DataScope) (DataScope, error) {
	if a.Type.breadth() >= b.Type.breadth() {
		if a.Type != b.Type {
			return a, nil
		}
		if a.Type != DataScopeMerchantSet {
			return a, nil
		}
		return unionMerchantSet(a, b)
	}
	if b.Type != DataScopeMerchantSet {
		return b, nil
	}
	return unionMerchantSet(b, a)
}

// unionMerchantSet 合并两个 merchant_set 的商户集合（去重、排序、限长）。
func unionMerchantSet(base, other DataScope) (DataScope, error) {
	ids := make([]string, 0, len(base.MerchantIDs)+len(other.MerchantIDs))
	ids = append(ids, base.MerchantIDs...)
	ids = append(ids, other.MerchantIDs...)
	ids = dedupeNonEmpty(ids)
	if len(ids) > MaxMerchantSetSize {
		return DataScope{}, errInvalid(ErrMerchantSetTooLarge,
			"merged merchant_set for resource %q exceeds %d merchants, got %d",
			base.Resource, MaxMerchantSetSize, len(ids))
	}
	return DataScope{Resource: base.Resource, Type: DataScopeMerchantSet, MerchantIDs: ids}, nil
}

// dedupeNonEmpty 去除空串与重复项并排序，保证落库与比较稳定。
func dedupeNonEmpty(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
