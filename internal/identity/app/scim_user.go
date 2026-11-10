package app

// SCIM User 用例：把 IdP 推送的 User 映射到 identity_account + identity_oauth_identity。
//
// 不变量：
//   - externalId 必填且是唯一幂等键，落 provider_subject_index + 密文；
//   - userName 必填，等价于 A 端登录主体，改名即主体变更（重算索引与密文）；
//   - 不设本地口令：SCIM 建出的账号只能经 OIDC / SAML 登录；
//   - active=false 与 /admin/v1/auth/accounts/{id}/disable 同语义（撤销会话 + token_version++）。

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// ScimUserInput 是 POST / PUT 的 User 入参（transport 已完成协议层校验）。
type ScimUserInput struct {
	// ExternalID IdP 侧用户 ID；必填，幂等键。
	ExternalID string
	// UserName A 端登录主体；必填。
	UserName string
	// DisplayName 由 name.givenName / familyName 拼接；可空。
	DisplayName string
	// Active nil 视为 true（RFC 7643 默认值）。
	Active *bool
}

// ScimUserPatch 是 PATCH 的增量变更；nil 字段表示不变。
type ScimUserPatch struct {
	ExternalID  *string
	UserName    *string
	DisplayName *string
	// ClearDisplayName 对应 remove displayName。
	ClearDisplayName bool
	Active           *bool
}

// ScimGroupRef 是 User.groups 的只读投影。
type ScimGroupRef struct {
	Value   string
	Display string
}

// ScimUser 是 User 视图。
type ScimUser struct {
	ID          string
	ExternalID  string
	UserName    string
	DisplayName string
	Active      bool
	Version     int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Groups      []ScimGroupRef
}

// ScimUserListQuery 是 GET /Users 的查询条件。
type ScimUserListQuery struct {
	UserName    string
	ExternalID  string
	DisplayName string
	ID          string
	Active      *bool
	// StartIndex 从 1 开始。
	StartIndex int
	// Count 为 0 表示只要 totalResults，不返回资源。
	Count int
}

// ScimUserPage 是分页结果。
type ScimUserPage struct {
	Users        []ScimUser
	Total        int
	StartIndex   int
	ItemsPerPage int
}

// GetUser 按主键返回 User；软删账号视为不存在。
func (s *ScimService) GetUser(ctx context.Context, id string) (*ScimUser, error) {
	account, err := s.loadScimUser(ctx, id)
	if err != nil {
		return nil, err
	}
	view, err := s.userView(ctx, account)
	if err != nil {
		return nil, err
	}
	return view, nil
}

// ListUsers 按受限过滤条件分页列出 A 端账号。
//
// 不支持通用表达式：只允许 userName / externalId / displayName / active / id 的 eq，
// 由 transport 解析后落到本方法的具名条件，收敛注入面。
func (s *ScimService) ListUsers(ctx context.Context, query ScimUserListQuery) (*ScimUserPage, error) {
	startIndex := query.StartIndex
	if startIndex < 1 {
		startIndex = 1
	}
	count := query.Count
	if count <= 0 {
		count = 0
	}
	if count > 100 {
		count = 100
	}
	limit := count
	if limit == 0 {
		limit = s.pageSize
	}
	offset := startIndex - 1

	var (
		accounts []*domain.Account
		total    int
	)
	switch {
	case strings.TrimSpace(query.ID) != "":
		account, err := s.accounts.FindByID(ctx, strings.TrimSpace(query.ID))
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return &ScimUserPage{StartIndex: startIndex}, nil
			}
			return nil, err
		}
		accounts, total = []*domain.Account{account}, 1
	case strings.TrimSpace(query.UserName) != "":
		account, err := s.accounts.FindBySubject(ctx, domain.ChannelAdmin, "", query.UserName)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return &ScimUserPage{StartIndex: startIndex}, nil
			}
			return nil, err
		}
		accounts, total = []*domain.Account{account}, 1
	case strings.TrimSpace(query.ExternalID) != "":
		identity, err := s.identities.FindByProviderSubject(ctx, s.providerRef, strings.TrimSpace(query.ExternalID))
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return &ScimUserPage{StartIndex: startIndex}, nil
			}
			return nil, apperr.Unavailable("scim identity store unavailable")
		}
		account, loadErr := s.accounts.FindByID(ctx, identity.AccountID)
		if loadErr != nil {
			if errors.Is(loadErr, apperr.ErrNotFound) {
				return &ScimUserPage{StartIndex: startIndex}, nil
			}
			return nil, loadErr
		}
		accounts, total = []*domain.Account{account}, 1
	default:
		filter := domain.AccountListFilter{
			Channel:          domain.ChannelAdmin,
			DisplayNameExact: strings.TrimSpace(query.DisplayName),
			Active:           query.Active,
			ExcludeDeleted:   true,
			Limit:            limit,
			Offset:           offset,
		}
		listed, listedTotal, err := s.accounts.List(ctx, filter)
		if err != nil {
			return nil, err
		}
		accounts, total = listed, listedTotal
	}

	// 单条命中路径仍需应用 active / displayName 过滤，避免「过滤器被静默忽略」。
	accounts = filterAccounts(accounts, query)
	if len(accounts) == 0 {
		if isSingleLookup(query) {
			total = 0
		}
		return &ScimUserPage{StartIndex: startIndex, Total: total}, nil
	}
	if count == 0 {
		return &ScimUserPage{StartIndex: startIndex, Total: total}, nil
	}
	views := make([]ScimUser, 0, len(accounts))
	for _, account := range accounts {
		if account.IsDeleted() {
			continue
		}
		view, viewErr := s.userView(ctx, account)
		if viewErr != nil {
			return nil, viewErr
		}
		views = append(views, *view)
	}
	return &ScimUserPage{
		Users:        views,
		Total:        total,
		StartIndex:   startIndex,
		ItemsPerPage: len(views),
	}, nil
}

// isSingleLookup 报告查询是否按唯一键定位（此时 total 恒为 0 或 1）。
func isSingleLookup(query ScimUserListQuery) bool {
	return strings.TrimSpace(query.ID) != "" ||
		strings.TrimSpace(query.UserName) != "" ||
		strings.TrimSpace(query.ExternalID) != ""
}

// filterAccounts 对单条命中结果应用剩余过滤条件。
func filterAccounts(accounts []*domain.Account, query ScimUserListQuery) []*domain.Account {
	out := make([]*domain.Account, 0, len(accounts))
	for _, account := range accounts {
		if account == nil || account.IsDeleted() {
			continue
		}
		if name := strings.TrimSpace(query.DisplayName); name != "" && account.DisplayName != name {
			continue
		}
		if query.Active != nil {
			disabled := account.Status == domain.AccountStatusDisabled
			if *query.Active == disabled {
				continue
			}
		}
		if name := strings.TrimSpace(query.UserName); name != "" &&
			account.Subject != domain.NormalizeSubject(name) {
			continue
		}
		out = append(out, account)
	}
	return out
}

// CreateUser 创建 A 端账号并写入 SCIM 外部身份映射。
func (s *ScimService) CreateUser(ctx context.Context, in ScimUserInput, traceID string) (*ScimUser, error) {
	if !s.autoProv {
		return nil, scimInvalidValue("auto provisioning is disabled; the account must sign in through SSO first")
	}
	externalID := strings.TrimSpace(in.ExternalID)
	userName := domain.NormalizeSubject(in.UserName)
	if externalID == "" {
		return nil, scimInvalidValue("externalId is required")
	}
	if userName == "" {
		return nil, scimInvalidValue("userName is required")
	}
	if len(userName) > domain.MaxSubjectLen {
		return nil, scimInvalidValue("userName is too long")
	}
	// 幂等键先查：已存在映射说明 IdP 重复创建，返回 409 而不是覆盖。
	if _, err := s.identities.FindByProviderSubject(ctx, s.providerRef, externalID); err == nil {
		return nil, scimUniqueness("externalId already exists")
	} else if !errors.Is(err, apperr.ErrNotFound) {
		return nil, apperr.Unavailable("scim identity store unavailable")
	}
	if _, err := s.accounts.FindBySubject(ctx, domain.ChannelAdmin, "", userName); err == nil {
		return nil, scimUniqueness("userName already exists")
	} else if !errors.Is(err, apperr.ErrNotFound) {
		return nil, err
	}

	now := s.clock.Now().UTC()
	accountID, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate account id failed", err)
	}
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID:      accountID,
		Channel: domain.ChannelAdmin,
		Subject: userName,
		Now:     now,
	})
	if err != nil {
		return nil, scimInvalidValue("userName is not a valid subject")
	}
	// IdP 推送即视为管理上可用；SCIM 建出的账号没有本地口令，只能经 SSO 登录。
	if err := account.Activate(now); err != nil {
		return nil, err
	}
	if err := s.applyDisplayName(ctx, account, in.DisplayName, now); err != nil {
		return nil, err
	}
	active := in.Active == nil || *in.Active
	if !active {
		if err := account.Disable(now); err != nil {
			return nil, err
		}
	}
	identityID, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate oauth identity id failed", err)
	}
	identity, err := domain.NewOAuthIdentity(domain.NewOAuthIdentityParams{
		ID:              identityID,
		AccountID:       account.ID,
		Provider:        s.providerRef,
		ProviderSubject: externalID,
		Now:             now,
	})
	if err != nil {
		return nil, scimInvalidValue("externalId is not valid")
	}
	if err := s.withinTx(ctx, func(txCtx context.Context) error {
		if saveErr := s.accounts.Save(txCtx, account, 0); saveErr != nil {
			if errors.Is(saveErr, apperr.ErrConflict) {
				return scimUniqueness("userName already exists")
			}
			return saveErr
		}
		if saveErr := s.identities.Save(txCtx, identity); saveErr != nil {
			if errors.Is(saveErr, apperr.ErrConflict) {
				return scimUniqueness("externalId already exists")
			}
			return apperr.Unavailable("scim identity store unavailable")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	s.emitAudit(ctx, audit.ActionAuthScimUserCreated, "", traceID, "scim_user", account.ID,
		audit.OutcomeSuccess, "", map[string]string{
			"provider":         s.provider,
			"external_id_hash": shortHash(externalID),
			"active":           boolLabel(active),
		})
	view, err := s.userView(ctx, account)
	if err != nil {
		return nil, err
	}
	return view, nil
}

// ReplaceUser 整体替换 User（PUT）：userName / externalId / displayName / active。
func (s *ScimService) ReplaceUser(
	ctx context.Context,
	id string,
	in ScimUserInput,
	ifMatch *int64,
	traceID string,
) (*ScimUser, error) {
	return s.updateUser(ctx, id, ScimUserPatch{
		ExternalID:  &in.ExternalID,
		UserName:    &in.UserName,
		DisplayName: &in.DisplayName,
		Active:      in.Active,
	}, ifMatch, traceID)
}

// PatchUser 增量更新 User（PATCH add / replace / remove）。
func (s *ScimService) PatchUser(
	ctx context.Context,
	id string,
	patch ScimUserPatch,
	ifMatch *int64,
	traceID string,
) (*ScimUser, error) {
	return s.updateUser(ctx, id, patch, ifMatch, traceID)
}

// updateUser 是 PUT / PATCH 的共同实现：先校验版本，再按字段合并后写入。
func (s *ScimService) updateUser(
	ctx context.Context,
	id string,
	patch ScimUserPatch,
	ifMatch *int64,
	traceID string,
) (*ScimUser, error) {
	account, err := s.loadScimUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if ifMatch != nil && *ifMatch != account.Version {
		return nil, scimPreconditionFailed("resource version does not match If-Match")
	}
	now := s.clock.Now().UTC()
	expectedVersion := account.Version
	wasActive := account.Status != domain.AccountStatusDisabled

	if patch.DisplayName != nil || patch.ClearDisplayName {
		name := ""
		if patch.DisplayName != nil {
			name = *patch.DisplayName
		}
		if err := s.applyDisplayName(ctx, account, name, now); err != nil {
			return nil, err
		}
	}
	if patch.UserName != nil {
		userName := domain.NormalizeSubject(*patch.UserName)
		if userName == "" {
			return nil, scimInvalidValue("userName is required")
		}
		if userName != account.Subject {
			existing, findErr := s.accounts.FindBySubject(ctx, domain.ChannelAdmin, "", userName)
			if findErr == nil && existing.ID != account.ID {
				return nil, scimUniqueness("userName already exists")
			}
			if findErr != nil && !errors.Is(findErr, apperr.ErrNotFound) {
				return nil, findErr
			}
			if rebindErr := account.RebindSubject(userName, now); rebindErr != nil {
				return nil, scimInvalidValue("userName cannot be updated for this account")
			}
		}
	}
	if patch.Active != nil {
		if *patch.Active {
			if err := account.Enable(now); err != nil {
				return nil, scimInvalidValue("account cannot be enabled in its current state")
			}
		} else if err := account.Disable(now); err != nil {
			return nil, err
		}
	}

	var newExternalID string
	var rebindExternal bool
	if patch.ExternalID != nil {
		newExternalID = strings.TrimSpace(*patch.ExternalID)
		if newExternalID == "" {
			return nil, scimInvalidValue("externalId is required")
		}
		current, loadErr := s.currentExternalID(ctx, account.ID)
		if loadErr != nil {
			return nil, loadErr
		}
		if current != newExternalID {
			other, findErr := s.identities.FindByProviderSubject(ctx, s.providerRef, newExternalID)
			if findErr == nil && other.AccountID != account.ID {
				return nil, scimUniqueness("externalId already exists")
			}
			if findErr != nil && !errors.Is(findErr, apperr.ErrNotFound) {
				return nil, apperr.Unavailable("scim identity store unavailable")
			}
			rebindExternal = true
		}
	}

	if err := s.withinTx(ctx, func(txCtx context.Context) error {
		if saveErr := s.accounts.Save(txCtx, account, expectedVersion); saveErr != nil {
			if errors.Is(saveErr, apperr.ErrConflict) {
				return scimUniqueness("account was modified concurrently")
			}
			return saveErr
		}
		if rebindExternal {
			if rebindErr := s.rebindExternalID(txCtx, account.ID, newExternalID, now); rebindErr != nil {
				return rebindErr
			}
		}
		if wasActive && account.Status == domain.AccountStatusDisabled {
			// 停用必须同时撤销全部会话，否则旧访问令牌在过期前仍可用（AC-P16-SCIM-3）。
			if _, revokeErr := s.sessions.RevokeByAccount(txCtx, account.ID,
				domain.RevokeReasonAccountDisabled, now); revokeErr != nil {
				return apperr.Internal("revoke account sessions failed", revokeErr)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if newExternalID == "" && (patch.ExternalID == nil || rebindExternal) {
		// 未变更或已成功重绑：审计记录变更后的实际外部 ID 摘要。
		if current, loadErr := s.currentExternalID(ctx, account.ID); loadErr == nil {
			newExternalID = current
		}
	}
	action := audit.ActionAuthScimUserUpdated
	if wasActive && account.Status == domain.AccountStatusDisabled {
		action = audit.ActionAuthScimUserDeact
	}
	s.emitAudit(ctx, action, "", traceID, "scim_user", account.ID, audit.OutcomeSuccess, "",
		map[string]string{
			"provider":         s.provider,
			"external_id_hash": hashExternalID(newExternalID),
			"active":           boolLabel(account.Status != domain.AccountStatusDisabled),
			"version_check":    versionCheckLabel(ifMatch),
		})
	view, err := s.userView(ctx, account)
	if err != nil {
		return nil, err
	}
	return view, nil
}

// DeleteUser 软删账号：撤销会话、清理组成员与角色投影；重复删除幂等返回。
func (s *ScimService) DeleteUser(ctx context.Context, id string, traceID string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return scimNotFound("user not found")
	}
	account, err := s.accounts.FindByID(ctx, trimmed)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) || errors.Is(err, apperr.ErrBadRequest) {
			return scimNotFound("user not found")
		}
		return err
	}
	if account.Channel != domain.ChannelAdmin {
		return scimNotFound("user not found")
	}
	if account.IsDeleted() {
		// 幂等：重复 DELETE 返回 204，不重复写审计与撤销。
		return nil
	}
	now := s.clock.Now().UTC()
	expectedVersion := account.Version
	groups, err := s.groups.ListByAccount(ctx, account.ID)
	if err != nil {
		return err
	}
	beforeRoles := scimRoleCodesOf(groups)
	if err := s.withinTx(ctx, func(txCtx context.Context) error {
		if deleteErr := account.Delete(now); deleteErr != nil {
			return deleteErr
		}
		if saveErr := s.accounts.Save(txCtx, account, expectedVersion); saveErr != nil {
			return saveErr
		}
		if _, revokeErr := s.sessions.RevokeByAccount(txCtx, account.ID,
			domain.RevokeReasonAccountDeleted, now); revokeErr != nil {
			return apperr.Internal("revoke account sessions failed", revokeErr)
		}
		for _, group := range groups {
			if removeErr := s.groups.RemoveMembers(txCtx, group.ID, []string{account.ID}); removeErr != nil {
				return removeErr
			}
		}
		if len(beforeRoles) > 0 {
			codes := make([]string, 0, len(beforeRoles))
			for code := range beforeRoles {
				codes = append(codes, code)
			}
			if revokeErr := s.roles.RevokeGroupRoles(txCtx, account.ID, s.scimActor(),
				"scim user deleted", codes); revokeErr != nil {
				return revokeErr
			}
		}
		return nil
	}); err != nil {
		return err
	}
	s.emitAudit(ctx, audit.ActionAuthScimUserDeleted, "", traceID, "scim_user", account.ID,
		audit.OutcomeSuccess, "", map[string]string{"provider": s.provider})
	return nil
}

// currentExternalID 返回账号在当前 provider 下的外部 ID；未绑定时为空串。
func (s *ScimService) currentExternalID(ctx context.Context, accountID string) (string, error) {
	identities, err := s.identities.ListByAccount(ctx, accountID)
	if err != nil {
		return "", apperr.Unavailable("scim identity store unavailable")
	}
	for _, identity := range identities {
		if identity.Provider == s.providerRef {
			return identity.ProviderSubject, nil
		}
	}
	return "", nil
}

// rebindExternalID 更换账号的 SCIM 外部 ID：删除旧映射后写入新映射。
//
// 密文的 AAD 绑定映射主键，因此不能原地改摘要，必须换一行。
func (s *ScimService) rebindExternalID(ctx context.Context, accountID, externalID string, now time.Time) error {
	if err := s.identities.Delete(ctx, accountID, s.providerRef); err != nil &&
		!errors.Is(err, apperr.ErrNotFound) {
		return apperr.Unavailable("scim identity store unavailable")
	}
	id, err := s.ids.NextString()
	if err != nil {
		return apperr.Internal("generate oauth identity id failed", err)
	}
	identity, err := domain.NewOAuthIdentity(domain.NewOAuthIdentityParams{
		ID:              id,
		AccountID:       accountID,
		Provider:        s.providerRef,
		ProviderSubject: externalID,
		Now:             now,
	})
	if err != nil {
		return scimInvalidValue("externalId is not valid")
	}
	if saveErr := s.identities.Save(ctx, identity); saveErr != nil {
		if errors.Is(saveErr, apperr.ErrConflict) {
			return scimUniqueness("externalId already exists")
		}
		return apperr.Unavailable("scim identity store unavailable")
	}
	return nil
}

// loadScimUser 按主键加载未删除的 A 端账号。
func (s *ScimService) loadScimUser(ctx context.Context, id string) (*domain.Account, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return nil, scimNotFound("user not found")
	}
	account, err := s.accounts.FindByID(ctx, trimmed)
	if err != nil {
		// 非数字主键在 SQL 仓储会返回 40000：对外统一按 404，避免泄露 ID 语法。
		if errors.Is(err, apperr.ErrNotFound) || errors.Is(err, apperr.ErrBadRequest) {
			return nil, scimNotFound("user not found")
		}
		return nil, err
	}
	if account.Channel != domain.ChannelAdmin || account.IsDeleted() {
		return nil, scimNotFound("user not found")
	}
	return account, nil
}

// applyDisplayName 写入展示昵称；超长按 rune 截断并计数，不整批失败。
func (s *ScimService) applyDisplayName(ctx context.Context, account *domain.Account, name string, now time.Time) error {
	trimmed := s.truncateRunes(ctx, name, domain.MaxDisplayNameLen)
	if err := account.RenameDisplayName(trimmed, now); err != nil {
		return scimInvalidValue("displayName is not valid")
	}
	return nil
}

// userView 组装 User 视图：外部 ID 与所属组均为只读投影。
func (s *ScimService) userView(ctx context.Context, account *domain.Account) (*ScimUser, error) {
	externalID, err := s.currentExternalID(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	groups, err := s.groups.ListByAccount(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	refs := make([]ScimGroupRef, 0, len(groups))
	for _, group := range groups {
		refs = append(refs, ScimGroupRef{Value: group.ID, Display: group.DisplayName})
	}
	return &ScimUser{
		ID:          account.ID,
		ExternalID:  externalID,
		UserName:    account.Subject,
		DisplayName: account.DisplayName,
		Active:      account.Status != domain.AccountStatusDisabled,
		Version:     account.Version,
		CreatedAt:   account.CreatedAt,
		UpdatedAt:   account.UpdatedAt,
		Groups:      refs,
	}, nil
}

// boolLabel 把布尔转换为审计属性字面量。
func boolLabel(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// versionCheckLabel 标记本次写入是否做了条件请求校验（RFC 7644 §3.14）。
func versionCheckLabel(ifMatch *int64) string {
	if ifMatch == nil {
		return "none"
	}
	return "if-match"
}
