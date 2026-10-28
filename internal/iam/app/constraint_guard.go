package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// ConstraintMode 控制 RBAC2 约束的落库行为。
type ConstraintMode string

const (
	// ConstraintModeOff 关闭约束校验，行为与引入 RBAC2 前一致。
	ConstraintModeOff ConstraintMode = "off"
	// ConstraintModeShadow 只计算并记录违规，不拦截写入（灰度默认）。
	ConstraintModeShadow ConstraintMode = "shadow"
	// ConstraintModeEnforce 违规时拒绝写入，返回 40900。
	ConstraintModeEnforce ConstraintMode = "enforce"
)

// ParseConstraintMode 归一化约束模式；空串按 shadow 处理。
func ParseConstraintMode(value string) (ConstraintMode, error) {
	switch ConstraintMode(strings.ToLower(strings.TrimSpace(value))) {
	case ConstraintModeOff:
		return ConstraintModeOff, nil
	case ConstraintModeShadow, "":
		return ConstraintModeShadow, nil
	case ConstraintModeEnforce:
		return ConstraintModeEnforce, nil
	default:
		return "", fmt.Errorf("unsupported iam.constraints.mode: %s", value)
	}
}

// ConstraintObserver 观测约束检查结果，供指标与告警使用。
//
// kind 为检查类别（bind / hierarchy / delete）或违规类型（sod_conflict 等）；
// result 为 pass / mode 值（shadow / enforce）/ error。禁止携带 role_id、
// subject_id 等高基数标签（docs/rbac-iam.md §11.2）。
type ConstraintObserver interface {
	OnConstraintCheck(ctx context.Context, kind, result string)
}

// ConstraintGuardConfig 是 RBAC2 约束检查器的装配参数。
type ConstraintGuardConfig struct {
	// Mode off | shadow | enforce；off 时不要求仓储。
	Mode ConstraintMode
	// Roles 用于展开有效角色闭包（绑定角色 + RBAC1 祖先）。
	Roles domain.RoleRepository
	// Bindings 用于角色基数统计与事务内锁定读取。
	Bindings domain.BindingRepository
	// SodGroups 互斥角色组。
	SodGroups domain.SodGroupRepository
	// Restrictions 角色前置约束。
	Restrictions domain.RoleConstraintRepository
	// Observer 可选的检查结果观测出口；nil 表示不采集。
	Observer ConstraintObserver
	// Logger 影子期告警；为空时使用 slog.Default。
	Logger *slog.Logger
}

// ConstraintGuard 在绑定写入前校验 RBAC2 约束。
//
// 判定基于「有效角色闭包」：绑定角色 + 全部 active 祖先。破玻璃角色整体豁免，
// 避免应急通道被互斥 / 前置 / 基数规则阻断。
type ConstraintGuard struct {
	mode         ConstraintMode
	roles        domain.RoleRepository
	bindings     domain.BindingRepository
	sodGroups    domain.SodGroupRepository
	restrictions domain.RoleConstraintRepository
	observer     ConstraintObserver
	log          *slog.Logger
}

// NewConstraintGuard 构造约束检查器；非 off 模式要求全部仓储就绪（fail-closed）。
func NewConstraintGuard(cfg ConstraintGuardConfig) (*ConstraintGuard, error) {
	mode := cfg.Mode
	if mode == "" {
		mode = ConstraintModeShadow
	}
	if mode != ConstraintModeOff {
		if cfg.Roles == nil || cfg.Bindings == nil || cfg.SodGroups == nil || cfg.Restrictions == nil {
			return nil, apperr.Internal("iam constraint repositories are required", nil)
		}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ConstraintGuard{
		mode:         mode,
		roles:        cfg.Roles,
		bindings:     cfg.Bindings,
		sodGroups:    cfg.SodGroups,
		restrictions: cfg.Restrictions,
		observer:     cfg.Observer,
		log:          logger,
	}, nil
}

// observe 上报一次检查结果；nil 接收者或未配置观测出口时静默跳过。
func (g *ConstraintGuard) observe(ctx context.Context, kind, result string) {
	if g == nil || g.observer == nil {
		return
	}
	g.observer.OnConstraintCheck(ctx, kind, result)
}

// Mode 返回当前约束模式；nil 接收者视为 off。
func (g *ConstraintGuard) Mode() ConstraintMode {
	if g == nil {
		return ConstraintModeOff
	}
	return g.mode
}

// Enabled 报告是否需要执行约束校验。
func (g *ConstraintGuard) Enabled() bool {
	return g != nil && g.mode != ConstraintModeOff
}

// CheckBind 计算绑定 role 后可能产生的 RBAC2 违规。
//
// existing 必须是事务内已锁定的主体有效绑定（ListBySubjectForUpdate 的返回值）：
// 互斥与前置判定要求同一主体范围内串行，否则并发绑定会各自通过。
func (g *ConstraintGuard) CheckBind(
	ctx context.Context,
	role *domain.Role,
	existing []*domain.Binding,
	at time.Time,
) ([]domain.ConstraintViolation, error) {
	if !g.Enabled() || role == nil || role.Type == domain.RoleTypeBreakglass {
		return nil, nil
	}

	target, err := g.roleClosure(ctx, role)
	if err != nil {
		return nil, err
	}
	held, err := g.heldClosure(ctx, role, existing)
	if err != nil {
		return nil, err
	}

	violations := make([]domain.ConstraintViolation, 0, 4)

	if role.MaxBindings != nil {
		count, countErr := g.bindings.CountEffectiveByRole(ctx, role.Channel, role.MerchantID, role.ID, at)
		if countErr != nil {
			return nil, countErr
		}
		if count >= *role.MaxBindings {
			violations = append(violations, domain.ConstraintViolation{
				Kind:    domain.ConstraintKindCardinality,
				RoleIDs: []string{role.ID},
				Detail: fmt.Sprintf("role %s already has %d effective bindings, limit %d",
					role.Code, count, *role.MaxBindings),
			})
		}
	}

	closureIDs := sortedKeys(target)
	constraints, err := g.restrictions.ListByRoles(ctx, closureIDs)
	if err != nil {
		return nil, err
	}
	for _, constraint := range constraints {
		if constraint.Empty() {
			continue
		}
		for _, requiredID := range constraint.RequiredRoleIDs {
			if _, ok := held[requiredID]; ok {
				continue
			}
			// 目标角色自身闭包内包含前置角色时，绑定即同时持有，视为满足。
			if _, ok := target[requiredID]; ok {
				continue
			}
			violations = append(violations, domain.ConstraintViolation{
				Kind:    domain.ConstraintKindPrerequisite,
				RoleIDs: []string{constraint.RoleID, requiredID},
				Detail:  fmt.Sprintf("role %s requires role %s", constraint.RoleID, requiredID),
			})
		}
	}

	groups, err := g.activeGroups(ctx, role.Channel, role.MerchantID, closureIDs)
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		members := make(map[string]struct{}, len(group.RoleIDs))
		for _, groupRoleID := range group.RoleIDs {
			if _, ok := target[groupRoleID]; ok {
				members[groupRoleID] = struct{}{}
				continue
			}
			if _, ok := held[groupRoleID]; ok {
				members[groupRoleID] = struct{}{}
			}
		}
		if len(members) <= group.MaxActiveRoles {
			continue
		}
		violations = append(violations, domain.ConstraintViolation{
			Kind:      domain.ConstraintKindSodConflict,
			GroupID:   group.ID,
			GroupCode: group.Code,
			RoleIDs:   sortedKeys(members),
			Detail: fmt.Sprintf("sod group %s allows %d active roles, binding would reach %d",
				group.Code, group.MaxActiveRoles, len(members)),
		})
	}

	sortViolations(violations)
	if len(violations) == 0 {
		g.observe(ctx, "bind", "pass")
	}
	return violations, nil
}

// CheckHierarchy 校验把 roleID 挂到 parentID 下是否会形成结构性互斥冲突。
//
// 改挂父角色会改变整棵子树的继承闭包，因此不仅检查角色自身，还要检查全部
// 未删除子孙：任一后代的有效闭包在某个活跃互斥组内超阈值，都视为冲突。
func (g *ConstraintGuard) CheckHierarchy(
	ctx context.Context,
	channel domain.Channel,
	merchantID, roleID, parentID string,
	roleType domain.RoleType,
) ([]domain.ConstraintViolation, error) {
	if !g.Enabled() || parentID == "" || roleType == domain.RoleTypeBreakglass {
		return nil, nil
	}
	chains, err := g.hierarchyChains(ctx, roleID, parentID)
	if err != nil {
		return nil, err
	}
	if len(chains) == 0 {
		return nil, nil
	}

	involved := make(map[string]struct{}, len(chains))
	for _, chain := range chains {
		for id := range chain {
			involved[id] = struct{}{}
		}
	}
	groups, err := g.activeGroups(ctx, channel, merchantID, sortedKeys(involved))
	if err != nil {
		return nil, err
	}

	violations := make([]domain.ConstraintViolation, 0, len(groups))
	for _, group := range groups {
		var (
			worstCount int
			worstRoles []string
		)
		for _, chain := range chains {
			members := make([]string, 0, len(chain))
			for _, groupRoleID := range group.RoleIDs {
				if _, ok := chain[groupRoleID]; ok {
					members = append(members, groupRoleID)
				}
			}
			slices.Sort(members)
			if len(members) > worstCount {
				worstCount = len(members)
				worstRoles = members
			}
		}
		if worstCount <= group.MaxActiveRoles {
			continue
		}
		violations = append(violations, domain.ConstraintViolation{
			Kind:      domain.ConstraintKindHierarchy,
			GroupID:   group.ID,
			GroupCode: group.Code,
			RoleIDs:   worstRoles,
			Detail: fmt.Sprintf("reparenting role %s would put %d roles of sod group %s in one closure",
				roleID, worstCount, group.Code),
		})
	}
	sortViolations(violations)
	if len(violations) == 0 {
		g.observe(ctx, "hierarchy", "pass")
	}
	return violations, nil
}

// DeleteBlockers 返回阻止软删角色的引用原因；空表示可以删除。
func (g *ConstraintGuard) DeleteBlockers(ctx context.Context, role *domain.Role) ([]string, error) {
	if !g.Enabled() || role == nil {
		return nil, nil
	}
	blockers := make([]string, 0, 3)

	groups, err := g.sodGroups.ListByRole(ctx, role.Channel, role.MerchantID, role.ID)
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		if group.IsActive() {
			blockers = append(blockers, "sod_group:"+group.Code)
		}
	}

	dependents, err := g.restrictions.ListDependents(ctx, role.ID)
	if err != nil {
		return nil, err
	}
	for _, dependentID := range dependents {
		blockers = append(blockers, "prerequisite_of:"+dependentID)
	}
	slices.Sort(blockers)
	if len(blockers) == 0 {
		g.observe(ctx, "delete", "pass")
	}
	return blockers, nil
}

// Decide 按模式处理违规：shadow 记录后放行，enforce 返回 40900。
func (g *ConstraintGuard) Decide(ctx context.Context, violations []domain.ConstraintViolation, logArgs ...any) error {
	if len(violations) == 0 || !g.Enabled() {
		return nil
	}
	for _, violation := range violations {
		g.observe(ctx, string(violation.Kind), string(g.mode))
	}
	if g.mode != ConstraintModeEnforce {
		args := append([]any{"violation_count", len(violations), "violations", summaries(violations)}, logArgs...)
		g.log.WarnContext(ctx, "iam rbac2 constraint violated (shadow mode)", args...)
		return nil
	}
	return newConstraintError(violations)
}

// DecideBlockers 按模式处理删除拦截原因：shadow 记录后放行，enforce 返回 40900。
func (g *ConstraintGuard) DecideBlockers(ctx context.Context, blockers []string, logArgs ...any) error {
	if len(blockers) == 0 || !g.Enabled() {
		return nil
	}
	g.observe(ctx, "delete", string(g.mode))
	if g.mode != ConstraintModeEnforce {
		args := append([]any{"blockers", blockers}, logArgs...)
		g.log.WarnContext(ctx, "iam rbac2 delete check failed (shadow mode)", args...)
		return nil
	}
	return apperr.Conflict("role is still referenced by " + strings.Join(blockers, ", "))
}

// HandleError 处理约束检查自身的错误：enforce 失败关闭，shadow / off 记录后放行。
func (g *ConstraintGuard) HandleError(ctx context.Context, err error, logArgs ...any) error {
	if err == nil || !g.Enabled() {
		return nil
	}
	g.observe(ctx, "check_error", "error")
	if g.mode != ConstraintModeEnforce {
		args := append([]any{"err", err}, logArgs...)
		g.log.WarnContext(ctx, "iam rbac2 constraint check failed (shadow mode)", args...)
		return nil
	}
	return err
}

// ConstraintError 携带违规明细，便于传输层 / 审计通过 errors.As 读取。
type ConstraintError struct {
	Violations []domain.ConstraintViolation
}

// Error 实现 error 接口。
func (e *ConstraintError) Error() string {
	return "rbac2 constraint violated: " + strings.Join(summaries(e.Violations), "; ")
}

// newConstraintError 构造稳定 40900 并挂接结构化违规明细。
func newConstraintError(violations []domain.ConstraintViolation) error {
	detail := &ConstraintError{Violations: violations}
	return apperr.New(apperr.CodeConflict, detail.Error(),
		errors.Join(apperr.ErrConflict, detail))
}

// roleClosure 返回角色自身与全部 active 祖先。
func (g *ConstraintGuard) roleClosure(ctx context.Context, role *domain.Role) (map[string]*domain.Role, error) {
	set := map[string]*domain.Role{role.ID: role}
	ancestors, err := g.roles.ListAncestors(ctx, role.ID)
	if err != nil {
		return nil, err
	}
	for _, ancestor := range ancestors {
		if !ancestor.IsActive() {
			continue
		}
		set[ancestor.ID] = ancestor
	}
	return set, nil
}

// heldClosure 返回主体当前生效绑定及其 active 祖先；破玻璃角色不参与约束。
func (g *ConstraintGuard) heldClosure(
	ctx context.Context,
	role *domain.Role,
	existing []*domain.Binding,
) (map[string]struct{}, error) {
	set := make(map[string]struct{}, len(existing))
	for _, binding := range existing {
		// 目标角色自身跳过：重复绑定走幂等分支，不应计入互斥。
		if binding.RoleID == role.ID {
			continue
		}
		held, err := g.roles.FindByID(ctx, binding.RoleID)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				continue
			}
			return nil, err
		}
		if held.Type == domain.RoleTypeBreakglass || !held.IsActive() ||
			held.Channel != role.Channel || held.MerchantID != role.MerchantID {
			continue
		}
		set[held.ID] = struct{}{}
		ancestors, err := g.roles.ListAncestors(ctx, held.ID)
		if err != nil {
			return nil, err
		}
		for _, ancestor := range ancestors {
			if !ancestor.IsActive() || ancestor.Type == domain.RoleTypeBreakglass {
				continue
			}
			set[ancestor.ID] = struct{}{}
		}
	}
	return set, nil
}

// activeGroups 返回指定角色集合所属的活跃互斥组，按组 ID 去重。
func (g *ConstraintGuard) activeGroups(
	ctx context.Context,
	channel domain.Channel,
	merchantID string,
	roleIDs []string,
) ([]*domain.SodGroup, error) {
	seen := make(map[string]*domain.SodGroup)
	for _, roleID := range roleIDs {
		groups, err := g.sodGroups.ListByRole(ctx, channel, merchantID, roleID)
		if err != nil {
			return nil, err
		}
		for _, group := range groups {
			if group.IsActive() {
				seen[group.ID] = group
			}
		}
	}
	out := make([]*domain.SodGroup, 0, len(seen))
	for _, group := range seen {
		out = append(out, group)
	}
	slices.SortFunc(out, func(a, b *domain.SodGroup) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// hierarchyChains 计算改挂父角色后，角色自身与全部子孙的有效闭包。
//
// 返回值按节点 ID 索引；闭包使用角色 ID 集合，不读取权限明细。
func (g *ConstraintGuard) hierarchyChains(
	ctx context.Context,
	roleID, parentID string,
) (map[string]map[string]struct{}, error) {
	parent, err := g.roles.FindByID(ctx, parentID)
	if err != nil {
		return nil, err
	}
	if parent.Type == domain.RoleTypeBreakglass {
		return nil, nil
	}
	parentAncestors, err := g.roles.ListAncestors(ctx, parentID)
	if err != nil {
		return nil, err
	}
	roleChain := map[string]struct{}{roleID: {}}
	roleChain[parent.ID] = struct{}{}
	for _, ancestor := range parentAncestors {
		roleChain[ancestor.ID] = struct{}{}
	}

	descendants, err := g.roles.ListDescendants(ctx, roleID)
	if err != nil {
		return nil, err
	}
	parentOf := make(map[string]string, len(descendants))
	for _, descendant := range descendants {
		parentOf[descendant.ID] = descendant.ParentID
	}

	chains := make(map[string]map[string]struct{}, len(descendants)+1)
	chains[roleID] = roleChain
	for _, descendant := range descendants {
		chain := map[string]struct{}{descendant.ID: {}}
		cursor := descendant.ParentID
		for depth := 0; depth < domain.MaxRoleHierarchyDepth && cursor != ""; depth++ {
			if cursor == roleID {
				for id := range roleChain {
					chain[id] = struct{}{}
				}
				break
			}
			chain[cursor] = struct{}{}
			next, ok := parentOf[cursor]
			if !ok {
				break
			}
			cursor = next
		}
		chains[descendant.ID] = chain
	}
	return chains, nil
}

// sortedKeys 返回 map 中排好序的键，保证违规明细与审计可复现。
func sortedKeys[T any](set map[string]T) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

// sortViolations 按类型、组编码与角色 ID 排序，避免 map 遍历导致输出抖动。
func sortViolations(violations []domain.ConstraintViolation) {
	slices.SortFunc(violations, func(a, b domain.ConstraintViolation) int {
		if byKind := strings.Compare(string(a.Kind), string(b.Kind)); byKind != 0 {
			return byKind
		}
		if byGroup := strings.Compare(a.GroupCode, b.GroupCode); byGroup != 0 {
			return byGroup
		}
		return strings.Compare(strings.Join(a.RoleIDs, ","), strings.Join(b.RoleIDs, ","))
	})
}

// summaries 把违规转为紧凑的可读文本，供日志与错误消息使用。
func summaries(violations []domain.ConstraintViolation) []string {
	out := make([]string, 0, len(violations))
	for _, violation := range violations {
		label := string(violation.Kind)
		if violation.GroupCode != "" {
			label += "(" + violation.GroupCode + ")"
		}
		if len(violation.RoleIDs) > 0 {
			label += "[" + strings.Join(violation.RoleIDs, ",") + "]"
		}
		out = append(out, label)
	}
	return out
}
