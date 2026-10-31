package app

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

// 账号管理操作默认审计原因；请求未提供原因时写入，避免空原因。
const (
	reasonAdminDisabled    = "admin_disabled"
	reasonAdminEnabled     = "admin_enabled"
	reasonAdminUnlocked    = "admin_unlocked"
	reasonAdminLocked      = "admin_locked"
	reasonAdminDeleted     = "admin_deleted"
	reasonAdminRestored    = "admin_restored"
	reasonAdminResetMFA    = "admin_reset_mfa"
	reasonAdminPasswordRst = "admin_password_reset"
	reasonUserPassword     = "user_password_change"
)

// 账号列表分页边界。
const (
	// accountListDefaultLimit 默认每页条数。
	accountListDefaultLimit = 20
	// accountListMaxLimit 每页最大条数。
	accountListMaxLimit = 100
)

// 管理员主动锁定时长边界。
const (
	minAdminLockDuration = time.Minute
	maxAdminLockDuration = 7 * 24 * time.Hour
)

// AccountService 是 A 端账号生命周期用例门面（docs/identity-authn.md §9、IDN-P0-02）。
//
// 复用 AuthService 的事务、审计与撤销失效设施，保证账号状态变更、会话撤销与审计
// 在同一事务内完成；快照失效在提交后执行，失败只告警不影响业务结果。
type AccountService struct {
	auth *AuthService
}

// NewAccountService 构造账号管理服务；AuthService 必填。
func NewAccountService(auth *AuthService) (*AccountService, error) {
	if auth == nil {
		return nil, apperr.Internal("account service requires auth service", nil)
	}
	return &AccountService{auth: auth}, nil
}

// revokeAccountSessions 撤销账号全部设备并上报指标；失败记录告警日志与失败指标。
func (s *AccountService) revokeAccountSessions(
	ctx context.Context,
	accountID string,
	reason domain.RevokeReason,
	now time.Time,
) error {
	if _, err := s.auth.sessions.RevokeByAccount(ctx, accountID, reason, now); err != nil {
		s.auth.logError("identity revoke account sessions failed",
			"account_ref", shortHash(accountID), "reason", reason.String(), "error", err)
		s.auth.observeSessionRevoke(ctx, reason, MetricResultFailure)
		return apperr.Internal("revoke account sessions failed", err)
	}
	s.auth.observeSessionRevoke(ctx, reason, MetricResultSuccess)
	return nil
}

// ChangePasswordInput 是用户自助改密入参。
type ChangePasswordInput struct {
	// Actor 当前登录上下文。
	Actor SessionActor
	// OldPassword 当前密码；只在本层短暂存在，禁止写日志。
	OldPassword string
	// NewPassword 新密码；须满足密码策略且不得与旧密码相同。
	NewPassword string
}

// AccountOperationInput 是管理员账号操作入参。
type AccountOperationInput struct {
	// Actor 执行操作的管理员；用于审计 actor_id。
	Actor SessionActor
	// TargetAccountID 被操作账号。
	TargetAccountID string
	// Reason 操作原因；写入审计 reason，禁止包含 PII。
	Reason string
	// MFACode 特权操作的二次 MFA 动态码或恢复码；admin_required=true 时必填。
	MFACode string
}

// passwordChangeProbe 是事务外完成旧密码校验与新密码哈希的结果（IDN-GAP-PERF-01）。
type passwordChangeProbe struct {
	// credentialHash 事务外读到的哈希，用于事务内检测并发改密。
	credentialHash string
	// encoded / algorithm 事务外计算的新哈希，事务内直接落库。
	encoded   string
	algorithm string
}

// probeChangePassword 在事务外完成旧密码校验与新密码哈希，避免 Argon2 占用数据库事务。
func (s *AccountService) probeChangePassword(ctx context.Context, in ChangePasswordInput) (*passwordChangeProbe, error) {
	loaded, err := s.auth.accounts.FindByID(ctx, in.Actor.AccountID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, invalidToken()
		}
		return nil, apperr.Internal("load account failed", err)
	}
	if loaded.Channel != s.auth.channel || loaded.Status != domain.AccountStatusActive {
		return nil, invalidToken()
	}
	credential, err := s.auth.credentials.FindByAccount(ctx, loaded.ID, domain.CredentialTypePassword)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, invalidCredentials()
		}
		return nil, apperr.Internal("load credential failed", err)
	}
	if _, err := s.auth.password.VerifyContext(ctx, credential.Hash, in.OldPassword); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.NewPassword) == in.OldPassword {
		return nil, apperr.BadRequest("new password must differ from the current password")
	}
	// SEC-06：历史密码复用必须在 Argon2 计算新哈希前拒绝。
	if err := s.auth.rejectRecentPasswordReuse(ctx, credential, in.NewPassword, s.auth.clock.Now().UTC()); err != nil {
		return nil, err
	}
	encoded, algorithm, err := s.auth.password.HashContext(ctx, in.NewPassword)
	if err != nil {
		return nil, err
	}
	return &passwordChangeProbe{
		credentialHash: credential.Hash,
		encoded:        encoded,
		algorithm:      algorithm,
	}, nil
}

// ChangePassword 校验旧密码后改密：递增 token_version、撤销全部会话并写审计。
//
// Argon2 校验与哈希在事务外完成，事务内只做哈希一致性复核与写入。
func (s *AccountService) ChangePassword(ctx context.Context, in ChangePasswordInput) error {
	if strings.TrimSpace(in.Actor.AccountID) == "" {
		return invalidToken()
	}
	probe, err := s.probeChangePassword(ctx, in)
	if err != nil {
		return err
	}
	now := s.auth.clock.Now().UTC()
	var account *domain.Account
	if err := s.auth.runTx(ctx, func(txCtx context.Context) error {
		loaded, err := s.auth.accounts.FindByID(txCtx, in.Actor.AccountID)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return invalidToken()
			}
			return apperr.Internal("load account failed", err)
		}
		if loaded.Channel != s.auth.channel || loaded.Status != domain.AccountStatusActive {
			return invalidToken()
		}
		credential, err := s.auth.credentials.FindByAccount(txCtx, loaded.ID, domain.CredentialTypePassword)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return invalidCredentials()
			}
			return apperr.Internal("load credential failed", err)
		}
		if credential.Hash != probe.credentialHash {
			// probe 与事务之间密码被并发修改：拒绝本次改密，避免覆盖新凭据。
			return apperr.Conflict("password credential was modified concurrently, please retry")
		}
		s.auth.recordPasswordChange(credential, now)
		if err := credential.ReplaceHash(probe.encoded, probe.algorithm, now); err != nil {
			return err
		}
		if err := s.auth.credentials.Save(txCtx, credential); err != nil {
			return apperr.Internal("save credential failed", err)
		}
		expected := loaded.Version
		if err := loaded.ChangePassword(now); err != nil {
			return err
		}
		if err := s.auth.accounts.Save(txCtx, loaded, expected); err != nil {
			return apperr.Internal("save account failed", err)
		}
		if err := s.revokeAccountSessions(txCtx, loaded.ID, domain.RevokeReasonPasswordChanged, now); err != nil {
			return err
		}
		if err := s.auth.emitAudit(txCtx, audit.ActionAuthPasswordChanged, loaded.ID, loaded.MerchantID, "account", loaded.ID,
			audit.OutcomeSuccess, reasonUserPassword, in.Actor.TraceID, nil); err != nil {
			return err
		}
		account = loaded
		return nil
	}); err != nil {
		return err
	}
	s.auth.invalidateAccountState(ctx, account.ID, accountStateOf(account))
	return nil
}

// Disable 停用目标账号：递增 token_version、撤销全部会话、写 auth.account.disabled。
//
// 已停用账号幂等成功且不重复写审计。
func (s *AccountService) Disable(ctx context.Context, in AccountOperationInput) error {
	now := s.auth.clock.Now().UTC()
	var account *domain.Account
	if err := s.auth.runTx(ctx, func(txCtx context.Context) error {
		loaded, err := s.auth.targetAccount(txCtx, in.TargetAccountID)
		if err != nil {
			return err
		}
		if loaded.Status == domain.AccountStatusDisabled {
			account = loaded
			return nil
		}
		expected := loaded.Version
		if err := loaded.Disable(now); err != nil {
			return err
		}
		if err := s.auth.accounts.Save(txCtx, loaded, expected); err != nil {
			return apperr.Internal("save account failed", err)
		}
		if err := s.revokeAccountSessions(txCtx, loaded.ID, domain.RevokeReasonAccountDisabled, now); err != nil {
			return err
		}
		if err := s.auth.emitAudit(txCtx, audit.ActionAuthAccountDisabled, loaded.ID, loaded.MerchantID, "account", loaded.ID,
			audit.OutcomeSuccess, operationReason(in.Reason, reasonAdminDisabled), in.Actor.TraceID, nil); err != nil {
			return err
		}
		account = loaded
		return nil
	}); err != nil {
		return err
	}
	s.auth.invalidateAccountState(ctx, account.ID, accountStateOf(account))
	return nil
}

// Enable 恢复停用账号；不恢复旧会话，用户必须重新登录。
func (s *AccountService) Enable(ctx context.Context, in AccountOperationInput) error {
	return s.changeStatus(ctx, in, func(account *domain.Account, now time.Time) error {
		switch account.Status {
		case domain.AccountStatusDisabled:
			return account.Enable(now)
		case domain.AccountStatusActive:
			return errNoTransition
		default:
			return apperr.Conflict("account is not disabled")
		}
	}, audit.ActionAuthAccountEnabled, reasonAdminEnabled)
}

// Unlock 管理员手动解锁；已解锁账号幂等成功且不重复写审计。
func (s *AccountService) Unlock(ctx context.Context, in AccountOperationInput) error {
	return s.changeStatus(ctx, in, func(account *domain.Account, now time.Time) error {
		switch account.Status {
		case domain.AccountStatusLocked:
			return account.Unlock(now)
		case domain.AccountStatusActive, domain.AccountStatusPending:
			return errNoTransition
		default:
			return apperr.Conflict("account is not locked")
		}
	}, audit.ActionAuthAccountUnlocked, reasonAdminUnlocked)
}

// errNoTransition 表示目标状态已满足，调用方按幂等成功处理。
var errNoTransition = errors.New("no transition required")

// changeStatus 执行「启用 / 解锁」这类不改 token_version 的状态迁移。
func (s *AccountService) changeStatus(
	ctx context.Context,
	in AccountOperationInput,
	apply func(*domain.Account, time.Time) error,
	action, reason string,
) error {
	now := s.auth.clock.Now().UTC()
	var account *domain.Account
	changed := false
	if err := s.auth.runTx(ctx, func(txCtx context.Context) error {
		loaded, err := s.auth.targetAccount(txCtx, in.TargetAccountID)
		if err != nil {
			return err
		}
		if err := s.auth.requireAdminStepUp(txCtx, in.Actor, in.MFACode); err != nil {
			return err
		}
		expected := loaded.Version
		if err := apply(loaded, now); err != nil {
			if errors.Is(err, errNoTransition) {
				account = loaded
				return nil
			}
			return err
		}
		if err := s.auth.accounts.Save(txCtx, loaded, expected); err != nil {
			return apperr.Internal("save account failed", err)
		}
		if err := s.auth.emitAudit(txCtx, action, loaded.ID, loaded.MerchantID, "account", loaded.ID,
			audit.OutcomeSuccess, operationReason(in.Reason, reason), in.Actor.TraceID, nil); err != nil {
			return err
		}
		account = loaded
		changed = true
		return nil
	}); err != nil {
		return err
	}
	// 未发生迁移时无需刷新快照，避免无意义写放大。
	if changed {
		s.auth.invalidateAccountState(ctx, account.ID, accountStateOf(account))
	}
	return nil
}

// ResetMFA 管理员重置目标账号的 MFA 绑定（用户丢失 TOTP 与恢复码的兜底路径）。
//
// 删除绑定、递增 token_version、撤销全部会话并写 auth.mfa.reset；不校验动态码。
func (s *AccountService) ResetMFA(ctx context.Context, in AccountOperationInput) error {
	if s.auth.mfa == nil {
		return apperr.Unavailable("mfa is not configured")
	}
	now := s.auth.clock.Now().UTC()
	var account *domain.Account
	if err := s.auth.runTx(ctx, func(txCtx context.Context) error {
		loaded, err := s.auth.targetAccount(txCtx, in.TargetAccountID)
		if err != nil {
			return err
		}
		if err := s.auth.requireAdminStepUp(txCtx, in.Actor, in.MFACode); err != nil {
			return err
		}
		if _, err := s.auth.mfa.FindByAccount(txCtx, loaded.ID); err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return apperr.NotFound("mfa enrollment not found")
			}
			return apperr.Internal("load mfa enrollment failed", err)
		}
		if err := s.auth.mfa.Delete(txCtx, loaded.ID); err != nil {
			return apperr.Internal("delete mfa enrollment failed", err)
		}
		expected := loaded.Version
		if err := loaded.BumpTokenVersion(now); err != nil {
			return err
		}
		if err := s.auth.accounts.Save(txCtx, loaded, expected); err != nil {
			return apperr.Internal("save account failed", err)
		}
		if err := s.revokeAccountSessions(txCtx, loaded.ID, domain.RevokeReasonAdmin, now); err != nil {
			return err
		}
		if err := s.auth.emitAudit(txCtx, audit.ActionAuthMFAReset, loaded.ID, loaded.MerchantID, "account", loaded.ID,
			audit.OutcomeSuccess, operationReason(in.Reason, reasonAdminResetMFA), in.Actor.TraceID, nil); err != nil {
			return err
		}
		account = loaded
		return nil
	}); err != nil {
		return err
	}
	s.auth.invalidateAccountState(ctx, account.ID, accountStateOf(account))
	return nil
}

// targetAccount 加载被操作账号并校验端别隔离；跨端与不存在统一 404，避免横向枚举。
func (s *AuthService) targetAccount(ctx context.Context, accountID string) (*domain.Account, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, apperr.BadRequest("account id is required")
	}
	account, err := s.accounts.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.NotFound("account not found")
		}
		return nil, apperr.Internal("load account failed", err)
	}
	if account.Channel != s.channel {
		return nil, apperr.NotFound("account not found")
	}
	if account.IsDeleted() {
		return nil, apperr.NotFound("account not found")
	}
	return account, nil
}

// accountStateOf 构造账号撤销快照。
func accountStateOf(account *domain.Account) AccountState {
	return AccountState{Version: account.TokenVersion, Status: account.Status}
}

// operationReason 取请求原因，空值时使用默认原因。
func operationReason(reason, fallback string) string {
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		return trimmed
	}
	return fallback
}

// AccountSummary 是账号列表项；不含凭据与令牌信息。
type AccountSummary struct {
	ID           string
	Subject      string
	Channel      domain.Channel
	Status       domain.AccountStatus
	MerchantID   string
	TokenVersion int64
	LockedUntil  *time.Time
	DeletedAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// AccountDetail 是账号详情；在摘要之上补充凭据与 MFA 状态。
type AccountDetail struct {
	AccountSummary
	MFAEnabled         bool
	MustChangePassword bool
	HasPassword        bool
	PhoneBound         bool
}

// AccountListInput 是账号列表查询入参。
type AccountListInput struct {
	// Actor 执行查询的管理员；用于审计 actor_id。
	Actor SessionActor
	// Status 状态过滤；空值表示不过滤。
	Status string
	// Keyword 主体关键词；空值表示不过滤。
	Keyword string
	// SubjectExact 规范化主体精确值；非空时优先于 Keyword 并走索引（PERF-07）。
	SubjectExact string
	// Limit / Offset 分页；由服务端钳制。
	Limit  int
	Offset int
}

// LockAccountInput 是管理员主动锁定入参。
type LockAccountInput struct {
	AccountOperationInput
	// Duration 锁定时长；≤0 使用默认锁定策略，范围 [1m, 7d]。
	Duration time.Duration
}

// List 分页列出本端账号（含软删），按创建时间倒序。
//
// 批量读取登录主体属于敏感操作，成功后写 auth.account.read（scope=list，SEC-08）。
func (s *AccountService) List(ctx context.Context, in AccountListInput) ([]AccountSummary, int, error) {
	if strings.TrimSpace(in.Actor.AccountID) == "" {
		return nil, 0, invalidToken()
	}
	status := domain.AccountStatus(strings.TrimSpace(in.Status))
	if status != "" {
		if err := status.Validate(); err != nil {
			return nil, 0, err
		}
	}
	limit := in.Limit
	if limit <= 0 {
		limit = accountListDefaultLimit
	}
	if limit > accountListMaxLimit {
		limit = accountListMaxLimit
	}
	offset := in.Offset
	if offset < 0 {
		offset = 0
	}
	exact := strings.TrimSpace(in.SubjectExact)
	if len(exact) > maxLoginSubjectBytes {
		return nil, 0, apperr.BadRequest("subject exceeds maximum length")
	}
	accounts, total, err := s.auth.accounts.List(ctx, domain.AccountListFilter{
		Channel:      s.auth.channel,
		Status:       status,
		Keyword:      strings.TrimSpace(in.Keyword),
		SubjectExact: exact,
		Limit:        limit,
		Offset:       offset,
	})
	if err != nil {
		return nil, 0, apperr.Internal("list accounts failed", err)
	}
	out := make([]AccountSummary, 0, len(accounts))
	for _, account := range accounts {
		out = append(out, accountSummaryOf(account))
	}
	attrs := map[string]string{
		"scope":  "list",
		"limit":  strconv.Itoa(limit),
		"offset": strconv.Itoa(offset),
	}
	if status != "" {
		attrs["status"] = status.String()
	}
	if in.Keyword != "" {
		// 只记录存在关键词，不写主体原文（避免审计二次留存 PII）。
		attrs["keyword_present"] = "true"
	}
	if exact != "" {
		attrs["subject_exact_present"] = "true"
	}
	if err := s.auth.emitAudit(ctx, audit.ActionAuthAccountRead, in.Actor.AccountID, in.Actor.MerchantID,
		"account", "list", audit.OutcomeSuccess, "", in.Actor.TraceID, attrs); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// Get 返回账号详情；软删账号同样可查，便于恢复。
//
// 读取用户 PII 属于敏感操作，成功读取后写 auth.account.read 审计。
func (s *AccountService) Get(ctx context.Context, actor SessionActor, targetAccountID string) (AccountDetail, error) {
	account, err := s.auth.loadAccountAny(ctx, targetAccountID)
	if err != nil {
		return AccountDetail{}, err
	}
	if err := s.auth.emitAudit(ctx, audit.ActionAuthAccountRead, actor.AccountID, account.MerchantID,
		"account", account.ID, audit.OutcomeSuccess, "", actor.TraceID, nil); err != nil {
		return AccountDetail{}, err
	}
	detail := AccountDetail{AccountSummary: accountSummaryOf(account), MustChangePassword: account.MustChangePassword}
	credentials, err := s.auth.credentials.ListByAccount(ctx, account.ID)
	if err != nil {
		return AccountDetail{}, apperr.Internal("list credentials failed", err)
	}
	for _, credential := range credentials {
		switch credential.Type {
		case domain.CredentialTypePassword:
			detail.HasPassword = true
		case domain.CredentialTypePhone:
			detail.PhoneBound = true
		}
	}
	if s.auth.mfa != nil {
		enrollment, mfaErr := s.auth.mfa.FindByAccount(ctx, account.ID)
		switch {
		case mfaErr == nil:
			detail.MFAEnabled = enrollment.IsConfirmed()
		case errors.Is(mfaErr, apperr.ErrNotFound):
			detail.MFAEnabled = false
		default:
			return AccountDetail{}, apperr.Internal("load mfa enrollment failed", mfaErr)
		}
	}
	return detail, nil
}

// Lock 管理员主动锁定账号；同时递增 token_version 并撤销全部会话。
func (s *AccountService) Lock(ctx context.Context, in LockAccountInput) error {
	now := s.auth.clock.Now().UTC()
	duration := in.Duration
	if duration <= 0 {
		duration = DefaultLockoutDuration
	}
	if duration < minAdminLockDuration {
		duration = minAdminLockDuration
	}
	if duration > maxAdminLockDuration {
		duration = maxAdminLockDuration
	}
	var account *domain.Account
	changed := false
	if err := s.auth.runTx(ctx, func(txCtx context.Context) error {
		loaded, err := s.auth.targetAccount(txCtx, in.TargetAccountID)
		if err != nil {
			return err
		}
		if err := s.auth.requireAdminStepUp(txCtx, in.Actor, in.MFACode); err != nil {
			return err
		}
		if loaded.Status == domain.AccountStatusDisabled {
			return apperr.Conflict("account is disabled")
		}
		expected := loaded.Version
		if loaded.Status == domain.AccountStatusLocked {
			if loaded.LockedUntil != nil && loaded.LockedUntil.After(now) {
				account = loaded
				return nil
			}
			// 锁定已过期：先归一为 active，否则 domain.Lock 会因状态不是 active/pending 拒绝。
			if _, err := loaded.UnlockIfExpired(now); err != nil {
				return err
			}
		}
		if err := loaded.Lock(now.Add(duration), now); err != nil {
			return err
		}
		if err := loaded.BumpTokenVersion(now); err != nil {
			return err
		}
		if err := s.auth.accounts.Save(txCtx, loaded, expected); err != nil {
			return apperr.Internal("save account failed", err)
		}
		if err := s.revokeAccountSessions(txCtx, loaded.ID, domain.RevokeReasonAdmin, now); err != nil {
			return err
		}
		if err := s.auth.emitAudit(txCtx, audit.ActionAuthAccountLocked, loaded.ID, loaded.MerchantID, "account", loaded.ID,
			audit.OutcomeFailure, operationReason(in.Reason, reasonAdminLocked), in.Actor.TraceID, nil); err != nil {
			return err
		}
		account = loaded
		changed = true
		return nil
	}); err != nil {
		return err
	}
	if changed {
		s.auth.invalidateAccountState(ctx, account.ID, accountStateOf(account))
	}
	return nil
}

// ResetPassword 管理员重置密码：生成一次性临时口令并置强制改密标记。
//
// 返回的临时口令只在本次响应出现，禁止写日志；用户首次登录只拿到改密票据。
// Argon2 哈希在事务外完成，避免占用数据库事务（IDN-GAP-PERF-01）。
func (s *AccountService) ResetPassword(ctx context.Context, in AccountOperationInput) (string, error) {
	// 先校验目标账号存在且属于本端，避免对无效目标执行 Argon2。
	if _, err := s.auth.targetAccount(ctx, in.TargetAccountID); err != nil {
		return "", err
	}
	temporary, err := generateTemporaryPassword(s.auth.temporaryPasswordLength)
	if err != nil {
		return "", err
	}
	encoded, algorithm, err := s.auth.password.HashContext(ctx, temporary)
	if err != nil {
		return "", err
	}
	now := s.auth.clock.Now().UTC()
	var account *domain.Account
	if err := s.auth.runTx(ctx, func(txCtx context.Context) error {
		loaded, err := s.auth.targetAccount(txCtx, in.TargetAccountID)
		if err != nil {
			return err
		}
		if err := s.auth.requireAdminStepUp(txCtx, in.Actor, in.MFACode); err != nil {
			return err
		}
		credential, err := s.auth.credentials.FindByAccount(txCtx, loaded.ID, domain.CredentialTypePassword)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return apperr.NotFound("password credential not found")
			}
			return apperr.Internal("load credential failed", err)
		}
		s.auth.recordPasswordChange(credential, now)
		if err := credential.ReplaceHash(encoded, algorithm, now); err != nil {
			return err
		}
		if err := s.auth.credentials.Save(txCtx, credential); err != nil {
			return apperr.Internal("save credential failed", err)
		}
		expected := loaded.Version
		if err := loaded.ResetPassword(now); err != nil {
			return err
		}
		if err := s.auth.accounts.Save(txCtx, loaded, expected); err != nil {
			return apperr.Internal("save account failed", err)
		}
		if err := s.revokeAccountSessions(txCtx, loaded.ID, domain.RevokeReasonAdmin, now); err != nil {
			return err
		}
		if err := s.auth.emitAudit(txCtx, audit.ActionAuthPasswordReset, loaded.ID, loaded.MerchantID, "account", loaded.ID,
			audit.OutcomeSuccess, operationReason(in.Reason, reasonAdminPasswordRst), in.Actor.TraceID, nil); err != nil {
			return err
		}
		account = loaded
		return nil
	}); err != nil {
		return "", err
	}
	s.auth.invalidateAccountState(ctx, account.ID, accountStateOf(account))
	return temporary, nil
}

// Delete 软删账号：递增 token_version、撤销全部会话，subject 仍占用唯一键。
func (s *AccountService) Delete(ctx context.Context, in AccountOperationInput) error {
	now := s.auth.clock.Now().UTC()
	var account *domain.Account
	if err := s.auth.runTx(ctx, func(txCtx context.Context) error {
		loaded, err := s.auth.targetAccount(txCtx, in.TargetAccountID)
		if err != nil {
			return err
		}
		if err := s.auth.requireAdminStepUp(txCtx, in.Actor, in.MFACode); err != nil {
			return err
		}
		expected := loaded.Version
		if err := loaded.Delete(now); err != nil {
			return err
		}
		if err := s.auth.accounts.Save(txCtx, loaded, expected); err != nil {
			return apperr.Internal("save account failed", err)
		}
		if err := s.revokeAccountSessions(txCtx, loaded.ID, domain.RevokeReasonAccountDisabled, now); err != nil {
			return err
		}
		if err := s.auth.emitAudit(txCtx, audit.ActionAuthAccountDeleted, loaded.ID, loaded.MerchantID, "account", loaded.ID,
			audit.OutcomeSuccess, operationReason(in.Reason, reasonAdminDeleted), in.Actor.TraceID, nil); err != nil {
			return err
		}
		account = loaded
		return nil
	}); err != nil {
		return err
	}
	s.auth.invalidateAccountState(ctx, account.ID, accountStateOf(account))
	return nil
}

// Restore 恢复软删账号；不恢复旧会话，token_version 不递减。
func (s *AccountService) Restore(ctx context.Context, in AccountOperationInput) error {
	now := s.auth.clock.Now().UTC()
	var account *domain.Account
	changed := false
	if err := s.auth.runTx(ctx, func(txCtx context.Context) error {
		loaded, err := s.auth.loadAccountAny(txCtx, in.TargetAccountID)
		if err != nil {
			return err
		}
		if err := s.auth.requireAdminStepUp(txCtx, in.Actor, in.MFACode); err != nil {
			return err
		}
		if loaded.DeletedAt == nil {
			account = loaded
			return nil
		}
		expected := loaded.Version
		if err := loaded.Restore(now); err != nil {
			return err
		}
		if err := s.auth.accounts.Save(txCtx, loaded, expected); err != nil {
			return apperr.Internal("save account failed", err)
		}
		if err := s.auth.emitAudit(txCtx, audit.ActionAuthAccountRestored, loaded.ID, loaded.MerchantID, "account", loaded.ID,
			audit.OutcomeSuccess, operationReason(in.Reason, reasonAdminRestored), in.Actor.TraceID, nil); err != nil {
			return err
		}
		account = loaded
		changed = true
		return nil
	}); err != nil {
		return err
	}
	if changed {
		s.auth.invalidateAccountState(ctx, account.ID, accountStateOf(account))
	}
	return nil
}

// ListTargetSessions 列出目标账号的设备会话（管理员视角均标记为非当前会话）。
func (s *AccountService) ListTargetSessions(ctx context.Context, actor SessionActor, targetAccountID, cursor string, limit int) (SessionPage, error) {
	account, err := s.auth.targetAccount(ctx, targetAccountID)
	if err != nil {
		return SessionPage{}, err
	}
	if err := s.auth.emitAudit(ctx, audit.ActionAuthSessionRead, actor.AccountID, account.MerchantID,
		"account", account.ID, audit.OutcomeSuccess, "", actor.TraceID, nil); err != nil {
		return SessionPage{}, err
	}
	return s.auth.listFamilies(ctx, account.ID, "", cursor, limit)
}

// RevokeTargetSession 撤销目标账号的指定设备（family）。
//
// 提交后写设备全部令牌代的撤销快照，使该设备已签发的 Access Token 在 ≤60s 内失效（IDN-P1-02）。
func (s *AccountService) RevokeTargetSession(ctx context.Context, in AccountOperationInput, familyID string) error {
	if strings.TrimSpace(familyID) == "" {
		return apperr.BadRequest("session id is required")
	}
	now := s.auth.clock.Now().UTC()
	var writes []sessionStateWrite
	if err := s.auth.runTx(ctx, func(txCtx context.Context) error {
		account, err := s.auth.targetAccount(txCtx, in.TargetAccountID)
		if err != nil {
			return err
		}
		if err := s.auth.requireAdminStepUp(txCtx, in.Actor, in.MFACode); err != nil {
			return err
		}
		family, err := s.auth.sessions.FindFamily(txCtx, familyID)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return apperr.NotFound("session not found")
			}
			return apperr.Internal("load session family failed", err)
		}
		if family.AccountID != account.ID {
			return apperr.NotFound("session not found")
		}
		chain, revokeErr := s.auth.revokeFamilyChain(txCtx, familyID, domain.RevokeReasonAdmin, now)
		if revokeErr != nil {
			return apperr.Internal("revoke session family failed", revokeErr)
		}
		writes = chain
		return s.auth.emitAudit(txCtx, audit.ActionAuthTokenRevoked, account.ID, account.MerchantID, "session", familyID,
			audit.OutcomeSuccess, string(domain.RevokeReasonAdmin), in.Actor.TraceID, nil)
	}); err != nil {
		return err
	}
	s.auth.markSessionsRevoked(ctx, writes)
	return nil
}

// RevokeTargetSessions 撤销目标账号全部会话，并递增 token_version 使访问令牌失效。
func (s *AccountService) RevokeTargetSessions(ctx context.Context, in AccountOperationInput) error {
	now := s.auth.clock.Now().UTC()
	var account *domain.Account
	if err := s.auth.runTx(ctx, func(txCtx context.Context) error {
		loaded, err := s.auth.targetAccount(txCtx, in.TargetAccountID)
		if err != nil {
			return err
		}
		if err := s.auth.requireAdminStepUp(txCtx, in.Actor, in.MFACode); err != nil {
			return err
		}
		expected := loaded.Version
		if err := loaded.BumpTokenVersion(now); err != nil {
			return err
		}
		if err := s.auth.accounts.Save(txCtx, loaded, expected); err != nil {
			return apperr.Internal("save account failed", err)
		}
		if err := s.revokeAccountSessions(txCtx, loaded.ID, domain.RevokeReasonAdmin, now); err != nil {
			return err
		}
		if err := s.auth.emitAudit(txCtx, audit.ActionAuthTokenRevoked, loaded.ID, loaded.MerchantID, "account", loaded.ID,
			audit.OutcomeSuccess, string(domain.RevokeReasonAdmin), in.Actor.TraceID, nil); err != nil {
			return err
		}
		account = loaded
		return nil
	}); err != nil {
		return err
	}
	s.auth.invalidateAccountState(ctx, account.ID, accountStateOf(account))
	return nil
}

// loadAccountAny 加载本端账号（允许软删），用于详情与恢复。
func (s *AuthService) loadAccountAny(ctx context.Context, accountID string) (*domain.Account, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, apperr.BadRequest("account id is required")
	}
	account, err := s.accounts.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.NotFound("account not found")
		}
		return nil, apperr.Internal("load account failed", err)
	}
	if account.Channel != s.channel {
		return nil, apperr.NotFound("account not found")
	}
	return account, nil
}

// accountSummaryOf 把领域账号转换为列表项。
func accountSummaryOf(account *domain.Account) AccountSummary {
	return AccountSummary{
		ID:           account.ID,
		Subject:      account.Subject,
		Channel:      account.Channel,
		Status:       account.Status,
		MerchantID:   account.MerchantID,
		TokenVersion: account.TokenVersion,
		LockedUntil:  account.LockedUntil,
		DeletedAt:    account.DeletedAt,
		CreatedAt:    account.CreatedAt,
		UpdatedAt:    account.UpdatedAt,
	}
}
