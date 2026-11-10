package app

// 本文件实现 SCIM 2.0 服务端用例的核心部分（docs/identity-authn.md §21.1）。
//
// 边界：
//   - 只做服务端：IdP 推送 User / Group；
//   - User 复用 identity_account + identity_oauth_identity，不新建用户表；
//   - Group 落 identity_scim_group(_member)，角色投影经 ScimRoleProvisioner 端口
//     交给 cmd 注入的 IAM 实现（身份域不 import IAM，§13.1）；
//   - 协议错误体（RFC 7644 §3.12）由 transport 序列化，本层只产出 ScimError。

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// SCIM 令牌与密钥格式（§21.1.2）。
const (
	// scimTokenPrefix 令牌前缀，便于日志与泄露扫描识别。
	scimTokenPrefix = "scim_"
	// scimTokenBytes 令牌随机字节数（32 字节 = 256 bit）。
	scimTokenBytes = 32
	// scimFingerprintPrefix 指纹前缀，仅用于展示。
	scimFingerprintPrefix = "sha256:"
	// scimFingerprintHexLen 指纹保留的十六进制字符数。
	scimFingerprintHexLen = 12
	// scimProviderRefPrefix 写入映射表的 provider 前缀，映射为 <prefix><provider>。
	scimProviderRefPrefix = "scim:"
	// scimActorPrefix 审计与绑定授权人使用的主体前缀。
	scimActorPrefix = "scim:"
)

// ScimError 是 RFC 7644 §3.12 协议错误，由 transport 直接序列化。
type ScimError struct {
	// Status HTTP 状态码。
	Status int
	// ScimType RFC 7644 §3.12 的 scimType；空表示不返回该字段。
	ScimType string
	// Detail 人类可读原因；禁止回显主体原文、令牌或成员列表。
	Detail string
}

// Error 实现 error；消息只含状态与原因，便于日志排障。
func (e *ScimError) Error() string {
	return fmt.Sprintf("scim error %d %s: %s", e.Status, e.ScimType, e.Detail)
}

// 常用 SCIM 错误构造器；detail 禁止包含 PII。
func scimInvalidSyntax(detail string) *ScimError {
	return &ScimError{Status: http.StatusBadRequest, ScimType: "invalidSyntax", Detail: detail}
}

func scimInvalidValue(detail string) *ScimError {
	return &ScimError{Status: http.StatusBadRequest, ScimType: "invalidValue", Detail: detail}
}

func scimInvalidPath(detail string) *ScimError {
	return &ScimError{Status: http.StatusBadRequest, ScimType: "invalidPath", Detail: detail}
}

func scimInvalidFilter(detail string) *ScimError {
	return &ScimError{Status: http.StatusBadRequest, ScimType: "invalidFilter", Detail: detail}
}

func scimNoTarget(detail string) *ScimError {
	return &ScimError{Status: http.StatusBadRequest, ScimType: "noTarget", Detail: detail}
}

func scimUniqueness(detail string) *ScimError {
	return &ScimError{Status: http.StatusConflict, ScimType: "uniqueness", Detail: detail}
}

func scimMutability(detail string) *ScimError {
	return &ScimError{Status: http.StatusBadRequest, ScimType: "mutability", Detail: detail}
}

func scimNotFound(detail string) *ScimError {
	return &ScimError{Status: http.StatusNotFound, ScimType: "", Detail: detail}
}

func scimUnauthorized(detail string) *ScimError {
	return &ScimError{Status: http.StatusUnauthorized, ScimType: "", Detail: detail}
}

func scimPreconditionFailed(detail string) *ScimError {
	return &ScimError{Status: http.StatusPreconditionFailed, ScimType: "", Detail: detail}
}

// asScimError 提取协议错误；非协议错误返回 ok=false，由 transport 按业务码兜底映射。
func asScimError(err error) (*ScimError, bool) {
	var target *ScimError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// ScimObserver 接收 SCIM 请求与投影观测（§21.1.5）。
type ScimObserver interface {
	// ObserveScimRequest 记录一次 SCIM 请求；result=success|error，成功时刷新最后成功时间。
	ObserveScimRequest(ctx context.Context, resource, method, result, reason string, duration time.Duration)
	// ObserveScimGroupMap 记录一次组成员投影；result=ok|unmapped。
	ObserveScimGroupMap(ctx context.Context, result string)
	// ObserveScimDisplayNameTruncated 记录一次 displayName 超长截断。
	ObserveScimDisplayNameTruncated(ctx context.Context)
}

// ScimRoleProvisioner 是角色投影端口，由 cmd 注入 IAM 实现。
//
// 语义：
//   - 只处理 channel=admin、subject_type=staff、merchant_id="" 的绑定；
//   - Grant 幂等；Revoke 只撤销该账号在指定角色上的绑定；
//   - 同一事务内调用（ctx 携带事务），失败必须回滚整次 SCIM 请求。
type ScimRoleProvisioner interface {
	// GrantGroupRoles 为账号绑定指定 A 端角色 code。
	GrantGroupRoles(ctx context.Context, accountID, actor string, roleCodes []string) error
	// RevokeGroupRoles 撤销账号在指定 A 端角色 code 上的绑定。
	RevokeGroupRoles(ctx context.Context, accountID, actor, reason string, roleCodes []string) error
	// ValidateRoleCodes 在装配期校验 group_role_map 中的角色存在、为 A 端且未停用。
	ValidateRoleCodes(ctx context.Context, roleCodes []string) error
}

// ScimServiceConfig 是 SCIM 用例的装配参数。
type ScimServiceConfig struct {
	Accounts   domain.AccountRepository
	Identities domain.OAuthIdentityRepository
	Groups     domain.ScimGroupRepository
	Tokens     domain.ScimTokenRepository
	Sessions   domain.SessionRepository
	IDs        IDGenerator
	Audit      audit.Emitter
	// Roles 角色投影端口；nil 表示未接线，映射组将失败关闭（不允许静默丢权限）。
	Roles ScimRoleProvisioner
	UoW   UnitOfWork
	Clock Clock
	// Logger 仅用于「已提交后失效失败」等告警。
	Logger *slog.Logger
	// Observer 观测端口；nil 时降级为无观测。
	Observer ScimObserver
	// Service 写入审计事件的 service 字段。
	Service string
	// Provider 配置的目录提供方名（不含 scim: 前缀）。
	Provider string
	// AutoProvision 未绑定时是否允许直接建号；false 时 POST /Users 返回 400。
	AutoProvision bool
	// GroupRoleMap SCIM displayName → A 端角色 code。
	GroupRoleMap map[string]string
	// PageSize 列表默认页大小。
	PageSize int
	// TokenGrace 旧令牌退役宽限；0 表示无宽限。
	TokenGrace time.Duration
}

// ScimService 承载 SCIM 令牌、User 与 Group 用例。
type ScimService struct {
	accounts     domain.AccountRepository
	identities   domain.OAuthIdentityRepository
	groups       domain.ScimGroupRepository
	tokens       domain.ScimTokenRepository
	sessions     domain.SessionRepository
	ids          IDGenerator
	audit        audit.Emitter
	roles        ScimRoleProvisioner
	uow          UnitOfWork
	clock        Clock
	logger       *slog.Logger
	observer     ScimObserver
	service      string
	provider     string
	providerRef  string
	autoProv     bool
	groupRoleMap map[string]string
	pageSize     int
	tokenGrace   time.Duration
}

// NewScimService 构造 SCIM 用例服务。
func NewScimService(cfg ScimServiceConfig) (*ScimService, error) {
	if cfg.Accounts == nil || cfg.Identities == nil || cfg.Groups == nil ||
		cfg.Tokens == nil || cfg.Sessions == nil || cfg.IDs == nil {
		return nil, apperr.Internal("scim service dependencies are required", nil)
	}
	provider := strings.TrimSpace(cfg.Provider)
	if provider == "" {
		return nil, apperr.Internal("scim provider is required", nil)
	}
	svc := &ScimService{
		accounts:     cfg.Accounts,
		identities:   cfg.Identities,
		groups:       cfg.Groups,
		tokens:       cfg.Tokens,
		sessions:     cfg.Sessions,
		ids:          cfg.IDs,
		audit:        cfg.Audit,
		roles:        cfg.Roles,
		uow:          cfg.UoW,
		clock:        cfg.Clock,
		logger:       cfg.Logger,
		observer:     cfg.Observer,
		service:      cfg.Service,
		provider:     provider,
		providerRef:  scimProviderRefPrefix + provider,
		autoProv:     cfg.AutoProvision,
		groupRoleMap: make(map[string]string, len(cfg.GroupRoleMap)),
		pageSize:     cfg.PageSize,
		tokenGrace:   cfg.TokenGrace,
	}
	if svc.clock == nil {
		svc.clock = realClock{}
	}
	if svc.uow == nil {
		svc.uow = NoopUnitOfWork{}
	}
	if svc.pageSize <= 0 || svc.pageSize > 100 {
		svc.pageSize = 50
	}
	for name, role := range cfg.GroupRoleMap {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		svc.groupRoleMap[key] = strings.TrimSpace(role)
	}
	if len(svc.groupRoleMap) > 0 && svc.roles == nil {
		// 配置了映射却没有投影实现：直接失败，避免「同步成功但没有授权」。
		return nil, apperr.Internal("scim group_role_map requires a role provisioner", nil)
	}
	return svc, nil
}

// Provider 返回配置的目录提供方名。
func (s *ScimService) Provider() string { return s.provider }

// PageSize 返回列表默认页大小。
func (s *ScimService) PageSize() int { return s.pageSize }

// Observer 返回观测端口；未注入时为 nil。
func (s *ScimService) Observer() ScimObserver { return s.observer }

// ScimPrincipal 是 SCIM 令牌认证结果。
type ScimPrincipal struct {
	// TokenID 令牌主键，写入审计与日志。
	TokenID string
	// Provider 令牌所属 provider。
	Provider string
}

// CreatedScimToken 是签发结果；Token 明文只在此处出现一次。
type CreatedScimToken struct {
	Token     *domain.ScimToken
	Plaintext string
}

// ScimTokenSummary 是管理端令牌视图（不含原文）。
type ScimTokenSummary struct {
	ID          string
	Provider    string
	Fingerprint string
	Status      domain.ScimTokenStatus
	ExpiresAt   *time.Time
	CreatedBy   string
	CreatedAt   time.Time
	RevokedAt   *time.Time
}

// CreateToken 签发 SCIM 令牌：每 provider 最多 2 个 active，超出时把最旧的转入退役宽限。
func (s *ScimService) CreateToken(ctx context.Context, actorID, traceID string) (*CreatedScimToken, error) {
	plaintext, hash, err := generateScimToken()
	if err != nil {
		return nil, err
	}
	id, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate scim token id failed", err)
	}
	now := s.clock.Now().UTC()
	token, err := domain.NewScimToken(domain.NewScimTokenParams{
		ID:          id,
		Provider:    s.provider,
		TokenHash:   hash,
		Fingerprint: scimFingerprint(hash),
		CreatedBy:   actorID,
		Now:         now,
	})
	if err != nil {
		return nil, err
	}
	if err := s.withinTx(ctx, func(txCtx context.Context) error {
		existing, listErr := s.tokens.ListByProvider(txCtx, s.provider)
		if listErr != nil {
			return listErr
		}
		active := make([]*domain.ScimToken, 0, len(existing))
		for _, item := range existing {
			if item.Status == domain.ScimTokenActive {
				active = append(active, item)
			}
		}
		// ListByProvider 按创建时间倒序：从尾部（最旧）开始退役，直到 active 数 < 2。
		for i := len(active) - 1; i >= 0 && len(active) >= 2; i-- {
			oldest := active[i]
			oldest.Retire(now, s.tokenGrace)
			if updateErr := s.tokens.UpdateStatus(txCtx, oldest); updateErr != nil {
				return updateErr
			}
			active = active[:i]
		}
		if createErr := s.tokens.Create(txCtx, token); createErr != nil {
			return createErr
		}
		return nil
	}); err != nil {
		return nil, err
	}
	s.emitAudit(ctx, audit.ActionAuthScimTokenCreated, actorID, traceID, "scim_token", token.ID,
		audit.OutcomeSuccess, "", map[string]string{"provider": s.provider, "fingerprint": token.Fingerprint})
	return &CreatedScimToken{Token: token, Plaintext: plaintext}, nil
}

// ListTokens 返回 provider 下全部令牌（含已吊销），不含原文。
func (s *ScimService) ListTokens(ctx context.Context) ([]ScimTokenSummary, error) {
	tokens, err := s.tokens.ListByProvider(ctx, s.provider)
	if err != nil {
		return nil, err
	}
	out := make([]ScimTokenSummary, 0, len(tokens))
	for _, token := range tokens {
		out = append(out, ScimTokenSummary{
			ID:          token.ID,
			Provider:    token.Provider,
			Fingerprint: token.Fingerprint,
			Status:      token.Status,
			ExpiresAt:   token.ExpiresAt,
			CreatedBy:   token.CreatedBy,
			CreatedAt:   token.CreatedAt,
			RevokedAt:   token.RevokedAt,
		})
	}
	return out, nil
}

// RevokeToken 吊销令牌；已吊销时幂等返回。
func (s *ScimService) RevokeToken(ctx context.Context, id, actorID, traceID string) error {
	token, err := s.tokens.FindByID(ctx, strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return scimNotFound("scim token not found")
		}
		return err
	}
	if token.Status == domain.ScimTokenRevoked {
		return nil
	}
	token.Revoke(s.clock.Now())
	if err := s.tokens.UpdateStatus(ctx, token); err != nil {
		return err
	}
	s.emitAudit(ctx, audit.ActionAuthScimTokenRevoked, actorID, traceID, "scim_token", token.ID,
		audit.OutcomeSuccess, "", map[string]string{"provider": s.provider, "fingerprint": token.Fingerprint})
	return nil
}

// AuthenticateToken 校验 Bearer 明文令牌；失败统一返回 401，不区分原因。
func (s *ScimService) AuthenticateToken(ctx context.Context, raw string) (*ScimPrincipal, error) {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, scimTokenPrefix) || len(value) > 128 {
		return nil, scimUnauthorized("invalid scim token")
	}
	hash := hashScimToken(value)
	token, err := s.tokens.FindByHash(ctx, s.provider, hash)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, scimUnauthorized("invalid scim token")
		}
		return nil, apperr.Unavailable("scim token store unavailable")
	}
	if !token.Usable(s.clock.Now()) {
		return nil, scimUnauthorized("scim token is not active")
	}
	return &ScimPrincipal{TokenID: token.ID, Provider: token.Provider}, nil
}

// withinTx 在事务内执行；未注入 UoW 时直接执行（内存模式）。
func (s *ScimService) withinTx(ctx context.Context, fn func(context.Context) error) error {
	if s.uow == nil {
		return fn(ctx)
	}
	return s.uow.WithinTx(ctx, fn)
}

// mappedRole 返回组展示名对应的角色 code；未映射返回空串。
func (s *ScimService) mappedRole(displayName string) string {
	return strings.TrimSpace(s.groupRoleMap[strings.TrimSpace(displayName)])
}

// emitAudit 写入 SCIM 审计事件；失败不改变业务结果，但记录告警。
func (s *ScimService) emitAudit(
	ctx context.Context,
	action, actorID, traceID, resourceType, resourceID string,
	outcome audit.Outcome,
	reason string,
	attrs map[string]string,
) {
	if s.audit == nil {
		return
	}
	eventID, err := s.ids.NextString()
	if err != nil {
		if s.logger != nil {
			s.logger.ErrorContext(ctx, "identity scim audit event id failed",
				"action", action, "error", err.Error())
		}
		return
	}
	actorType := "system"
	if actorID != "" {
		actorType = "staff"
	}
	event := audit.Event{
		EventID:      eventID,
		Action:       action,
		ActorID:      actorID,
		ActorType:    actorType,
		Channel:      string(domain.ChannelAdmin),
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Outcome:      outcome,
		OccurredAt:   s.clock.Now().UTC(),
		TraceID:      normalizeTraceID(traceID),
		Service:      s.service,
		Reason:       reason,
		Attrs:        attrs,
	}
	if err := s.audit.Emit(ctx, event); err != nil && s.logger != nil {
		s.logger.ErrorContext(ctx, "identity scim audit emit failed",
			"action", action, "error", err.Error())
	}
}

// generateScimToken 生成 scim_<32 字节 base64url> 明文与 SHA-256 摘要。
func generateScimToken() (plaintext, hash string, err error) {
	buf := make([]byte, scimTokenBytes)
	if _, readErr := rand.Read(buf); readErr != nil {
		return "", "", apperr.Internal("generate scim token failed", readErr)
	}
	plaintext = scimTokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
	return plaintext, hashScimToken(plaintext), nil
}

// hashScimToken 返回令牌的 SHA-256 十六进制摘要；库内只存该值。
func hashScimToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

// scimFingerprint 返回展示用指纹；长度不足以反推明文。
func scimFingerprint(hash string) string {
	if len(hash) < scimFingerprintHexLen {
		return scimFingerprintPrefix + hash
	}
	return scimFingerprintPrefix + hash[:scimFingerprintHexLen]
}

// truncateRunes 按 rune 截断展示名，避免中文昵称被字节截断成乱码。
func (s *ScimService) truncateRunes(ctx context.Context, value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	if utf8.RuneCountInString(trimmed) <= limit {
		return trimmed
	}
	runes := []rune(trimmed)
	if s.observer != nil {
		s.observer.ObserveScimDisplayNameTruncated(ctx)
	}
	return string(runes[:limit])
}

// scimRoleCodesOf 返回组集合对应的角色 code（去重、忽略未映射组）。
func scimRoleCodesOf(groups []*domain.ScimGroup) map[string]struct{} {
	out := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		if group == nil {
			continue
		}
		if role := strings.TrimSpace(group.MappedRole); role != "" {
			out[role] = struct{}{}
		}
	}
	return out
}

// roleDelta 计算 before → after 的增删集合。
func roleDelta(before, after map[string]struct{}) (added, removed []string) {
	for role := range after {
		if _, ok := before[role]; !ok {
			added = append(added, role)
		}
	}
	for role := range before {
		if _, ok := after[role]; !ok {
			removed = append(removed, role)
		}
	}
	return added, removed
}

// scimActor 返回 SCIM 写入绑定时的授权人标识。
func (s *ScimService) scimActor() string { return scimActorPrefix + s.provider }

// hashExternalID 返回外部 ID 的 SHA-256 摘要前 16 位十六进制。
//
// 审计只允许出现摘要：externalId 往往是邮箱或 UPN，明文截断仍是 PII 泄露。
func hashExternalID(externalID string) string {
	trimmed := strings.TrimSpace(externalID)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:])[:16]
}
