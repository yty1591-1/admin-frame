package app

// SCIM Group 用例与角色投影（docs/identity-authn.md §21.1.3「角色投影」）。
//
// 投影规则：
//   - 组展示名经 group_role_map 映射为 A 端角色 code；未映射组只同步成员关系；
//   - 成员变更后按「账号当前全部组 → 期望角色集合」重算，只对差集增删，
//     避免多组映射同一角色时互相撤销；
//   - 绑定写入与 SCIM 资源写入在同一事务内提交（ctx 携带事务），
//     映射角色不可用时整请求失败，不留半成品。

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// ScimGroupInput 是 POST / PUT 的 Group 入参。
type ScimGroupInput struct {
	ExternalID  string
	DisplayName string
	// Members 成员标识：账号 ID 或 externalId；解析失败整请求 400。
	Members []string
}

// ScimGroupPatch 是 PATCH 的增量变更；nil 字段表示不变。
type ScimGroupPatch struct {
	ExternalID  *string
	DisplayName *string
	// ReplaceMembers 非 nil 表示整体替换成员集合。
	ReplaceMembers []string
	// AddMembers / RemoveMembers 增量变更；与 ReplaceMembers 互斥（上层已校验）。
	AddMembers    []string
	RemoveMembers []string
}

// ScimGroupMemberRef 是 Group.members 视图。
type ScimGroupMemberRef struct {
	Value   string
	Display string
}

// ScimGroup 是 Group 视图；Members 由 identity_scim_group_member 反查。
type ScimGroup struct {
	ID          string
	ExternalID  string
	DisplayName string
	MappedRole  string
	Version     int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Members     []ScimGroupMemberRef
}

// ScimGroupSummary 是管理端组视图（含成员数量）。
type ScimGroupSummary struct {
	ID          string
	ExternalID  string
	DisplayName string
	MappedRole  string
	MemberCount int
	Version     int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ScimGroupQuery 是 GET /Groups 的查询条件。
type ScimGroupQuery struct {
	DisplayName string
	ExternalID  string
	ID          string
	StartIndex  int
	Count       int
}

// ScimGroupPage 是组分页结果。
type ScimGroupPage struct {
	Groups       []ScimGroup
	Total        int
	StartIndex   int
	ItemsPerPage int
}

// GetGroup 按主键返回组与成员。
func (s *ScimService) GetGroup(ctx context.Context, id string) (*ScimGroup, error) {
	group, err := s.loadScimGroup(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.groupView(ctx, group)
}

// ListGroups 按受限过滤条件分页列出组。
func (s *ScimService) ListGroups(ctx context.Context, query ScimGroupQuery) (*ScimGroupPage, error) {
	startIndex := query.StartIndex
	if startIndex < 1 {
		startIndex = 1
	}
	count := query.Count
	if count <= 0 {
		count = s.pageSize
	}
	if count > 100 {
		count = 100
	}
	if id := strings.TrimSpace(query.ID); id != "" {
		group, err := s.loadScimGroup(ctx, id)
		if err != nil {
			if _, ok := asScimError(err); ok {
				return &ScimGroupPage{StartIndex: startIndex}, nil
			}
			return nil, err
		}
		if query.DisplayName != "" && group.DisplayName != query.DisplayName {
			return &ScimGroupPage{StartIndex: startIndex}, nil
		}
		if query.ExternalID != "" && group.ExternalID != query.ExternalID {
			return &ScimGroupPage{StartIndex: startIndex}, nil
		}
		view, viewErr := s.groupView(ctx, group)
		if viewErr != nil {
			return nil, viewErr
		}
		return &ScimGroupPage{
			Groups: []ScimGroup{*view}, Total: 1, StartIndex: startIndex, ItemsPerPage: 1,
		}, nil
	}
	groups, total, err := s.groups.List(ctx, domain.ScimGroupFilter{
		Provider:    s.provider,
		DisplayName: strings.TrimSpace(query.DisplayName),
		ExternalID:  strings.TrimSpace(query.ExternalID),
		Limit:       count,
		Offset:      startIndex - 1,
	})
	if err != nil {
		return nil, err
	}
	views := make([]ScimGroup, 0, len(groups))
	for _, group := range groups {
		view, viewErr := s.groupView(ctx, group)
		if viewErr != nil {
			return nil, viewErr
		}
		views = append(views, *view)
	}
	return &ScimGroupPage{
		Groups: views, Total: total, StartIndex: startIndex, ItemsPerPage: len(views),
	}, nil
}

// ListGroupSummaries 返回管理端组列表（含成员数量），供 A 端 v1 端点使用。
func (s *ScimService) ListGroupSummaries(ctx context.Context, startIndex, count int) ([]ScimGroupSummary, int, error) {
	page, err := s.ListGroups(ctx, ScimGroupQuery{StartIndex: startIndex, Count: count})
	if err != nil {
		return nil, 0, err
	}
	ids := make([]string, 0, len(page.Groups))
	for _, group := range page.Groups {
		ids = append(ids, group.ID)
	}
	counts, err := s.groups.CountMembers(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	out := make([]ScimGroupSummary, 0, len(page.Groups))
	for _, group := range page.Groups {
		out = append(out, ScimGroupSummary{
			ID:          group.ID,
			ExternalID:  group.ExternalID,
			DisplayName: group.DisplayName,
			MappedRole:  group.MappedRole,
			MemberCount: counts[group.ID],
			Version:     group.Version,
			CreatedAt:   group.CreatedAt,
			UpdatedAt:   group.UpdatedAt,
		})
	}
	return out, page.Total, nil
}

// CreateGroup 创建组并建立成员关系；映射角色可用时同时投影授权。
func (s *ScimService) CreateGroup(ctx context.Context, in ScimGroupInput, traceID string) (*ScimGroup, error) {
	externalID := strings.TrimSpace(in.ExternalID)
	displayName := strings.TrimSpace(in.DisplayName)
	if externalID == "" {
		return nil, scimInvalidValue("externalId is required")
	}
	if displayName == "" {
		return nil, scimInvalidValue("displayName is required")
	}
	if _, err := s.groups.FindByExternalID(ctx, s.provider, externalID); err == nil {
		return nil, scimUniqueness("externalId already exists")
	} else if !errors.Is(err, apperr.ErrNotFound) {
		return nil, err
	}
	if _, err := s.groups.FindByDisplayName(ctx, s.provider, displayName); err == nil {
		return nil, scimUniqueness("displayName already exists")
	} else if !errors.Is(err, apperr.ErrNotFound) {
		return nil, err
	}
	accountIDs, err := s.resolveMemberIDs(ctx, in.Members)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now().UTC()
	id, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate scim group id failed", err)
	}
	mappedRole := s.mappedRole(displayName)
	group, err := domain.NewScimGroup(domain.NewScimGroupParams{
		ID:          id,
		Provider:    s.provider,
		ExternalID:  externalID,
		DisplayName: displayName,
		MappedRole:  mappedRole,
		Now:         now,
	})
	if err != nil {
		return nil, scimInvalidValue("group fields are not valid")
	}
	beforeRoles, err := s.rolesSnapshot(ctx, accountIDs)
	if err != nil {
		return nil, err
	}
	members := make([]*domain.ScimGroupMember, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		members = append(members, &domain.ScimGroupMember{
			GroupID: group.ID, AccountID: accountID, CreatedAt: now,
		})
	}
	if err := s.withinTx(ctx, func(txCtx context.Context) error {
		if createErr := s.groups.Create(txCtx, group); createErr != nil {
			if errors.Is(createErr, apperr.ErrConflict) {
				return scimUniqueness("group already exists")
			}
			return createErr
		}
		if len(members) > 0 {
			if addErr := s.groups.AddMembers(txCtx, members); addErr != nil {
				return addErr
			}
		}
		return s.applyRoles(txCtx, accountIDs, beforeRoles, "scim group created")
	}); err != nil {
		return nil, err
	}
	s.observeGroupMap(ctx, mappedRole)
	s.emitAudit(ctx, audit.ActionAuthScimGroupUpdated, "", traceID, "scim_group", group.ID,
		audit.OutcomeSuccess, "create", map[string]string{
			"provider":     s.provider,
			"group_id":     group.ID,
			"member_delta": strconv.Itoa(len(members)),
		})
	return s.groupView(ctx, group)
}

// ReplaceGroup 整体替换组（PUT）：展示名、外部 ID 与成员集合。
func (s *ScimService) ReplaceGroup(
	ctx context.Context,
	id string,
	in ScimGroupInput,
	ifMatch *int64,
	traceID string,
) (*ScimGroup, error) {
	return s.updateGroup(ctx, id, ScimGroupPatch{
		ExternalID:  &in.ExternalID,
		DisplayName: &in.DisplayName,
		// PUT 是整体替换：空成员也必须表达为「清空」，因此显式构造非 nil 切片。
		ReplaceMembers: append([]string{}, in.Members...),
	}, ifMatch, traceID)
}

// PatchGroup 增量更新组（PATCH）。
func (s *ScimService) PatchGroup(
	ctx context.Context,
	id string,
	patch ScimGroupPatch,
	ifMatch *int64,
	traceID string,
) (*ScimGroup, error) {
	return s.updateGroup(ctx, id, patch, ifMatch, traceID)
}

// updateGroup 是 PUT / PATCH 的共同实现。
func (s *ScimService) updateGroup(
	ctx context.Context,
	id string,
	patch ScimGroupPatch,
	ifMatch *int64,
	traceID string,
) (*ScimGroup, error) {
	group, err := s.loadScimGroup(ctx, id)
	if err != nil {
		return nil, err
	}
	if ifMatch != nil && *ifMatch != group.Version {
		return nil, scimPreconditionFailed("resource version does not match If-Match")
	}
	// 乐观锁基准必须在任何字段变更前捕获：Rename / RebindExternalID 会递增版本。
	expectedVersion := group.Version
	currentMembers, err := s.groups.ListMembers(ctx, group.ID)
	if err != nil {
		return nil, err
	}
	currentIDs := make([]string, 0, len(currentMembers))
	for _, member := range currentMembers {
		currentIDs = append(currentIDs, member.AccountID)
	}

	if patch.DisplayName != nil {
		name := strings.TrimSpace(*patch.DisplayName)
		if name == "" {
			return nil, scimInvalidValue("displayName is required")
		}
		if name != group.DisplayName {
			existing, findErr := s.groups.FindByDisplayName(ctx, s.provider, name)
			if findErr == nil && existing.ID != group.ID {
				return nil, scimUniqueness("displayName already exists")
			}
			if findErr != nil && !errors.Is(findErr, apperr.ErrNotFound) {
				return nil, findErr
			}
		}
		if err := group.Rename(name, s.mappedRole(name), s.clock.Now()); err != nil {
			return nil, scimInvalidValue("displayName is not valid")
		}
	}
	if patch.ExternalID != nil {
		externalID := strings.TrimSpace(*patch.ExternalID)
		if externalID == "" {
			return nil, scimInvalidValue("externalId is required")
		}
		if externalID != group.ExternalID {
			existing, findErr := s.groups.FindByExternalID(ctx, s.provider, externalID)
			if findErr == nil && existing.ID != group.ID {
				return nil, scimUniqueness("externalId already exists")
			}
			if findErr != nil && !errors.Is(findErr, apperr.ErrNotFound) {
				return nil, findErr
			}
			if err := group.RebindExternalID(externalID, s.clock.Now()); err != nil {
				return nil, scimInvalidValue("externalId is not valid")
			}
		}
	}

	var addIDs, removeIDs []string
	switch {
	case patch.ReplaceMembers != nil:
		desired, resolveErr := s.resolveMemberIDs(ctx, patch.ReplaceMembers)
		if resolveErr != nil {
			return nil, resolveErr
		}
		desiredSet := make(map[string]struct{}, len(desired))
		for _, accountID := range desired {
			desiredSet[accountID] = struct{}{}
		}
		currentSet := make(map[string]struct{}, len(currentIDs))
		for _, accountID := range currentIDs {
			currentSet[accountID] = struct{}{}
		}
		for _, accountID := range desired {
			if _, ok := currentSet[accountID]; !ok {
				addIDs = append(addIDs, accountID)
			}
		}
		for _, accountID := range currentIDs {
			if _, ok := desiredSet[accountID]; !ok {
				removeIDs = append(removeIDs, accountID)
			}
		}
	default:
		addIDs, err = s.resolveMemberIDs(ctx, patch.AddMembers)
		if err != nil {
			return nil, err
		}
		removeIDs, err = s.resolveMemberIDs(ctx, patch.RemoveMembers)
		if err != nil {
			return nil, err
		}
	}

	// 角色投影需要「变更前」快照：成员并集在变更前的组集合。
	affected := unionIDs(currentIDs, addIDs, removeIDs)
	beforeRoles, err := s.rolesSnapshot(ctx, affected)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now().UTC()
	if err := s.withinTx(ctx, func(txCtx context.Context) error {
		if updateErr := s.groups.Update(txCtx, group, expectedVersion); updateErr != nil {
			if errors.Is(updateErr, apperr.ErrConflict) {
				return scimUniqueness("group was modified concurrently")
			}
			return updateErr
		}
		if len(addIDs) > 0 {
			members := make([]*domain.ScimGroupMember, 0, len(addIDs))
			for _, accountID := range addIDs {
				members = append(members, &domain.ScimGroupMember{
					GroupID: group.ID, AccountID: accountID, CreatedAt: now,
				})
			}
			if addErr := s.groups.AddMembers(txCtx, members); addErr != nil {
				return addErr
			}
		}
		if len(removeIDs) > 0 {
			if removeErr := s.groups.RemoveMembers(txCtx, group.ID, removeIDs); removeErr != nil {
				return removeErr
			}
		}
		return s.applyRoles(txCtx, affected, beforeRoles, "scim group membership changed")
	}); err != nil {
		return nil, err
	}
	s.observeGroupMap(ctx, group.MappedRole)
	s.emitAudit(ctx, audit.ActionAuthScimGroupUpdated, "", traceID, "scim_group", group.ID,
		audit.OutcomeSuccess, "update", map[string]string{
			"provider":     s.provider,
			"group_id":     group.ID,
			"member_delta": strconv.Itoa(len(addIDs) - len(removeIDs)),
		})
	return s.groupView(ctx, group)
}

// DeleteGroup 软删组、清空成员并回收该组带来的角色投影。
func (s *ScimService) DeleteGroup(ctx context.Context, id, traceID string) error {
	group, err := s.groups.FindByID(ctx, strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil
		}
		return err
	}
	if group.IsDeleted() {
		// 幂等：重复 DELETE 返回 204。
		return nil
	}
	members, err := s.groups.ListMembers(ctx, group.ID)
	if err != nil {
		return err
	}
	accountIDs := make([]string, 0, len(members))
	for _, member := range members {
		accountIDs = append(accountIDs, member.AccountID)
	}
	beforeRoles, err := s.rolesSnapshot(ctx, accountIDs)
	if err != nil {
		return err
	}
	expectedVersion := group.Version
	now := s.clock.Now().UTC()
	if err := s.withinTx(ctx, func(txCtx context.Context) error {
		if deleteErr := group.Delete(now); deleteErr != nil {
			return deleteErr
		}
		if saveErr := s.groups.SoftDelete(txCtx, group, expectedVersion); saveErr != nil {
			if errors.Is(saveErr, apperr.ErrConflict) {
				return scimUniqueness("group was modified concurrently")
			}
			return saveErr
		}
		if removeErr := s.groups.RemoveAllMembers(txCtx, group.ID); removeErr != nil {
			return removeErr
		}
		return s.applyRoles(txCtx, accountIDs, beforeRoles, "scim group deleted")
	}); err != nil {
		return err
	}
	s.emitAudit(ctx, audit.ActionAuthScimGroupUpdated, "", traceID, "scim_group", group.ID,
		audit.OutcomeSuccess, "delete", map[string]string{
			"provider":     s.provider,
			"group_id":     group.ID,
			"member_delta": strconv.Itoa(-len(accountIDs)),
		})
	return nil
}

// loadScimGroup 加载未删除的组；不存在或已软删返回 404。
func (s *ScimService) loadScimGroup(ctx context.Context, id string) (*domain.ScimGroup, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return nil, scimNotFound("group not found")
	}
	group, err := s.groups.FindByID(ctx, trimmed)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) || errors.Is(err, apperr.ErrBadRequest) {
			return nil, scimNotFound("group not found")
		}
		return nil, err
	}
	if group.IsDeleted() {
		return nil, scimNotFound("group not found")
	}
	return group, nil
}

// groupView 组装组视图：成员 value 使用账号 ID，display 使用登录主体。
func (s *ScimService) groupView(ctx context.Context, group *domain.ScimGroup) (*ScimGroup, error) {
	members, err := s.groups.ListMembers(ctx, group.ID)
	if err != nil {
		return nil, err
	}
	refs := make([]ScimGroupMemberRef, 0, len(members))
	for _, member := range members {
		account, loadErr := s.accounts.FindByID(ctx, member.AccountID)
		if loadErr != nil {
			if errors.Is(loadErr, apperr.ErrNotFound) {
				// 账号已不存在：跳过而不是整组失败，避免历史脏数据阻断同步。
				continue
			}
			return nil, loadErr
		}
		refs = append(refs, ScimGroupMemberRef{Value: account.ID, Display: account.Subject})
	}
	return &ScimGroup{
		ID:          group.ID,
		ExternalID:  group.ExternalID,
		DisplayName: group.DisplayName,
		MappedRole:  group.MappedRole,
		Version:     group.Version,
		CreatedAt:   group.CreatedAt,
		UpdatedAt:   group.UpdatedAt,
		Members:     refs,
	}, nil
}

// resolveMemberIDs 把成员标识（账号 ID 或 externalId）解析为账号 ID。
//
// 任一标识解析失败即整请求 400 invalidValue：宁可让 IdP 重试整批，
// 也不能静默丢成员导致权限缺失。
func (s *ScimService) resolveMemberIDs(ctx context.Context, values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		accountID, err := s.resolveMemberID(ctx, trimmed)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[accountID]; dup {
			continue
		}
		seen[accountID] = struct{}{}
		out = append(out, accountID)
	}
	return out, nil
}

// resolveMemberID 解析单个成员标识。
func (s *ScimService) resolveMemberID(ctx context.Context, value string) (string, error) {
	if account, err := s.accounts.FindByID(ctx, value); err == nil {
		if account.Channel == domain.ChannelAdmin && !account.IsDeleted() {
			return account.ID, nil
		}
		return "", scimInvalidValue("member is not an active admin account")
	} else if !errors.Is(err, apperr.ErrNotFound) && !errors.Is(err, apperr.ErrBadRequest) {
		// 40000 表示入参不是雪花 ID（例如 externalId），继续走外部身份解析。
		return "", err
	}
	identity, err := s.identities.FindByProviderSubject(ctx, s.providerRef, value)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return "", scimInvalidValue("member is not found")
		}
		return "", apperr.Unavailable("scim identity store unavailable")
	}
	account, err := s.accounts.FindByID(ctx, identity.AccountID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return "", scimInvalidValue("member is not found")
		}
		return "", err
	}
	if account.Channel != domain.ChannelAdmin || account.IsDeleted() {
		return "", scimInvalidValue("member is not an active admin account")
	}
	return account.ID, nil
}

// rolesSnapshot 记录账号集合在变更前的角色集合。
func (s *ScimService) rolesSnapshot(ctx context.Context, accountIDs []string) (map[string]map[string]struct{}, error) {
	out := make(map[string]map[string]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		groups, err := s.groups.ListByAccount(ctx, accountID)
		if err != nil {
			return nil, err
		}
		out[accountID] = scimRoleCodesOf(groups)
	}
	return out, nil
}

// applyRoles 按变更后的组成员关系重算期望角色，只对差集增删绑定。
func (s *ScimService) applyRoles(
	ctx context.Context,
	accountIDs []string,
	before map[string]map[string]struct{},
	reason string,
) error {
	if len(accountIDs) == 0 {
		return nil
	}
	for _, accountID := range accountIDs {
		groups, err := s.groups.ListByAccount(ctx, accountID)
		if err != nil {
			return err
		}
		after := scimRoleCodesOf(groups)
		added, removed := roleDelta(before[accountID], after)
		if len(added) > 0 {
			if grantErr := s.roles.GrantGroupRoles(ctx, accountID, s.scimActor(), added); grantErr != nil {
				return scimInvalidValue("mapped role is not available")
			}
		}
		if len(removed) > 0 {
			if revokeErr := s.roles.RevokeGroupRoles(ctx, accountID, s.scimActor(), reason, removed); revokeErr != nil {
				return scimInvalidValue("mapped role cannot be revoked")
			}
		}
	}
	return nil
}

// observeGroupMap 记录组映射结果；未映射组不产生授权。
func (s *ScimService) observeGroupMap(ctx context.Context, mappedRole string) {
	if s.observer == nil {
		return
	}
	if strings.TrimSpace(mappedRole) == "" {
		s.observer.ObserveScimGroupMap(ctx, "unmapped")
		return
	}
	s.observer.ObserveScimGroupMap(ctx, "ok")
}

// unionIDs 合并去重账号 ID 集合。
func unionIDs(lists ...[]string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 8)
	for _, list := range lists {
		for _, id := range list {
			if id == "" {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}
