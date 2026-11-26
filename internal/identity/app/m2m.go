package app

// 本文件实现 M2M 机器身份用例（docs/identity-authn.md §21.2）。
//
// 边界：
//   - 只做 client_credentials；不签发 refresh token，不支持 token exchange / on-behalf-of；
//   - 只代表服务，不绑定人：令牌不携带 roles / merchant_id，不得访问用户资源；
//   - 令牌按端别隔离：一次只为 admin / biz / c 之一签发。

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// M2M 用例哨兵错误；transport 据此映射 RFC 6749 §5.2 错误体。
var (
	// ErrM2MInvalidClient 客户端不存在 / 已停用 / 密钥错误 / 密钥过期，统一对外不可区分。
	ErrM2MInvalidClient = errors.New("invalid_client")
	// ErrM2MInvalidScope 申请 scope 缺失、越权或不在全局白名单内。
	ErrM2MInvalidScope = errors.New("invalid_scope")
)

// M2M 凭据长度（§21.2.4）。
const (
	// m2mClientIDBytes client_id 随机部分字节数（base64url 后 22 字符）。
	m2mClientIDBytes = 16
	// m2mSecretBytes client_secret 随机字节数（32 字节 = 256 bit）。
	m2mSecretBytes = 32
	// m2mClientIDPrefix client_id 前缀，便于日志识别类型。
	m2mClientIDPrefix = "svc_"
	// m2mFingerprintPrefix 指纹前缀，仅用于展示。
	m2mFingerprintPrefix = "sha256:"
	// m2mFingerprintHexLen 指纹保留的十六进制字符数。
	m2mFingerprintHexLen = 12
)

// m2mDummySecretHash 是客户端不存在时用于假比对的固定摘要。
//
// 目的：让「客户端不存在」与「密钥错误」在返回前都执行一次常数时间比较，
// 避免通过响应耗时枚举 client_id（AC-P16-M2M-1）。
const m2mDummySecretHash = "9f2f1b6d5b0a4c3e8d7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d1e0f9a8b7c6d5e" //nolint:gosec // G101 误报：假比对用的摘要常量，非真实凭据。

// M2MServiceConfig 是 M2M 用例的装配参数。
type M2MServiceConfig struct {
	// Clients 客户端仓储；必填。
	Clients domain.M2MClientRepository
	// Secrets 密钥仓储；必填。
	Secrets domain.M2MSecretRepository
	// Tokens 令牌服务；必填（使用 IssueClientToken）。
	Tokens *TokenService
	// IDs 雪花 ID 生成器；必填。
	IDs IDGenerator
	// Audit 审计写入端口；nil 时跳过（仅本地 / 单测）。
	Audit audit.Emitter
	// ClientStates 客户端撤销快照端口；nil 时不失效快照（下次校验自然回源）。
	ClientStates ClientStateStore
	// RevocationStateTTL 客户端快照最大陈旧度；≤0 使用默认值。
	RevocationStateTTL time.Duration
	// Channel 进程固定端别；签发令牌的 aud / channel 取该值。
	Channel domain.Channel
	// TokenTTL 机器令牌有效期；≤0 使用 DefaultAccountStateTTL 之外的默认 15m。
	TokenTTL time.Duration
	// RotateGrace 旧 secret 退役宽限；≤0 表示无宽限（旧密钥立即失效）。
	RotateGrace time.Duration
	// MaxSecretAge secret 最长生命周期；仅用于标记 rotation_due 告警，不自动失效。
	MaxSecretAge time.Duration
	// AllowedScopes 全局权限码白名单；申请 scope 必须同时命中客户端与全局白名单。
	AllowedScopes []string
	// Service 写入审计事件的 service 字段。
	Service string
	// Logger 仅用于「业务已提交、快照失效失败」的告警。
	Logger *slog.Logger
	// Observer 接收签发指标；可为 nil（降级为无观测）。
	Observer M2MObserver
	// Clock 可注入时钟（测试用）。
	Clock Clock
}

// M2MObserver 接收 M2M 令牌签发观测（§21.2.5）。
type M2MObserver interface {
	// ObserveM2MToken 记录一次签发尝试；result=issued|denied，reason 为粗粒度原因。
	ObserveM2MToken(ctx context.Context, clientID, channel, result, reason string, duration time.Duration)
	// ObserveM2MSecretAge 记录本次认证所用 secret 的年龄；超 max_secret_age 由告警规则判定。
	ObserveM2MSecretAge(ctx context.Context, clientID string, age time.Duration)
}

// M2MService 承载机器客户端生命周期与令牌签发用例。
type M2MService struct {
	clients      domain.M2MClientRepository
	secrets      domain.M2MSecretRepository
	tokens       *TokenService
	ids          IDGenerator
	audit        audit.Emitter
	clientStates ClientStateStore
	stateTTL     time.Duration
	channel      domain.Channel
	tokenTTL     time.Duration
	rotateGrace  time.Duration
	maxSecretAge time.Duration
	allowedScope map[string]struct{}
	service      string
	logger       *slog.Logger
	observer     M2MObserver
	clock        Clock
}

// NewM2MService 构造 M2M 用例服务。
func NewM2MService(cfg M2MServiceConfig) (*M2MService, error) {
	if cfg.Clients == nil || cfg.Secrets == nil || cfg.Tokens == nil || cfg.IDs == nil {
		return nil, apperr.Internal("m2m service dependencies are required", nil)
	}
	if err := cfg.Channel.Validate(); err != nil {
		return nil, err
	}
	svc := &M2MService{
		clients:      cfg.Clients,
		secrets:      cfg.Secrets,
		tokens:       cfg.Tokens,
		ids:          cfg.IDs,
		audit:        cfg.Audit,
		clientStates: cfg.ClientStates,
		stateTTL:     clampAccountStateTTL(cfg.RevocationStateTTL),
		channel:      cfg.Channel,
		tokenTTL:     cfg.TokenTTL,
		rotateGrace:  cfg.RotateGrace,
		maxSecretAge: cfg.MaxSecretAge,
		allowedScope: make(map[string]struct{}, len(cfg.AllowedScopes)),
		service:      cfg.Service,
		logger:       cfg.Logger,
		observer:     cfg.Observer,
		clock:        cfg.Clock,
	}
	if svc.clock == nil {
		svc.clock = realClock{}
	}
	for _, scope := range cfg.AllowedScopes {
		if trimmed := strings.TrimSpace(scope); trimmed != "" {
			svc.allowedScope[trimmed] = struct{}{}
		}
	}
	return svc, nil
}

// CreateM2MClientInput 是创建客户端的入参。
type CreateM2MClientInput struct {
	// Name 展示名；必填。
	Name string
	// Owner 归属团队 / 系统。
	Owner string
	// Channels 允许取令牌的端别；空表示仅当前进程端别。
	Channels []domain.Channel
	// Scopes 申请的权限码集合；必须 ⊆ 配置白名单。
	Scopes []string
	// ActorID 操作人主体 ID。
	ActorID string
	// TraceID 链路 ID，写入审计。
	TraceID string
}

// CreatedM2MClient 是创建结果；ClientSecret 只在此处出现一次。
type CreatedM2MClient struct {
	// Client 客户端聚合（不含任何密钥材料）。
	Client *domain.M2MClient
	// ClientSecret 明文密钥；禁止写入日志与审计。
	ClientSecret string
}

// RotatedM2MSecret 是轮换结果。
type RotatedM2MSecret struct {
	// ClientID 客户端标识。
	ClientID string
	// ClientSecret 新明文密钥；只出现一次。
	ClientSecret string
	// Fingerprint 展示用指纹。
	Fingerprint string
	// ExpiresAt 旧密钥宽限到期时间；零值表示无宽限。
	ExpiresAt time.Time
}

// M2MToken 是签发结果（RFC 6749 §5.1）。
type M2MToken struct {
	// AccessToken 令牌原文。
	AccessToken string
	// ExpiresIn 有效期秒数。
	ExpiresIn int64
	// Scopes 实际授予的权限码（与申请一致，不做收窄）。
	Scopes []string
}

// IssueTokenInput 是 client_credentials 签发入参。
type IssueTokenInput struct {
	// ClientID 客户端标识；来自 Basic 或表单。
	ClientID string
	// ClientSecret 客户端密钥明文；只用于本次校验。
	ClientSecret string
	// Scope 空格分隔的申请权限码；必填（不提供隐式默认值）。
	Scope string
	// TraceID 链路 ID。
	TraceID string
}

// CreateClient 创建机器客户端并返回一次性密钥。
func (s *M2MService) CreateClient(ctx context.Context, input CreateM2MClientInput) (*CreatedM2MClient, error) {
	now := s.clock.Now().UTC()
	id, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate client id failed", err)
	}
	clientID, err := generateM2MClientID()
	if err != nil {
		return nil, err
	}
	secret, err := generateM2MSecret()
	if err != nil {
		return nil, err
	}
	channels := input.Channels
	if len(channels) == 0 {
		channels = []domain.Channel{s.channel}
	}
	client := &domain.M2MClient{
		ID:              id,
		ClientID:        clientID,
		Name:            input.Name,
		Status:          domain.M2MClientActive,
		AllowedChannels: channels,
		AllowedScopes:   input.Scopes,
		TokenVersion:    1,
		Owner:           input.Owner,
		CreatedBy:       input.ActorID,
		Version:         1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	domain.NormalizeM2MClient(client)
	if err := client.Validate(); err != nil {
		return nil, err
	}
	if err := s.checkScopes(client.AllowedScopes); err != nil {
		return nil, err
	}
	hash := HashM2MSecret(secret)
	secretID, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate secret id failed", err)
	}
	record := &domain.M2MSecret{
		ID:          secretID,
		ClientID:    client.ClientID,
		SecretHash:  hash,
		Fingerprint: M2MSecretFingerprint(hash),
		Status:      domain.M2MSecretCurrent,
		CreatedAt:   now,
	}
	if err := record.Validate(); err != nil {
		return nil, err
	}
	if err := s.clients.Save(ctx, client, 0); err != nil {
		return nil, err
	}
	if err := s.secrets.Save(ctx, record); err != nil {
		// 客户端已落库但密钥写入失败：立即软删客户端，避免留下无法认证的孤儿。
		s.softDeleteOrphan(ctx, client, input.ActorID, input.TraceID)
		return nil, err
	}
	s.emitAudit(ctx, audit.ActionAuthM2MClientCreated, input.ActorID, input.TraceID, client.ClientID,
		audit.OutcomeSuccess, "", map[string]string{
			"channel": string(s.channel),
			"scopes":  strconv.Itoa(len(client.AllowedScopes)),
		})
	return &CreatedM2MClient{Client: client, ClientSecret: secret}, nil
}

// ListClients 分页列出客户端。
func (s *M2MService) ListClients(ctx context.Context, filter domain.M2MClientListFilter) ([]*domain.M2MClient, int, error) {
	return s.clients.List(ctx, filter)
}

// RotateSecret 轮换客户端密钥：新密钥立即生效，旧密钥进入 retiring 宽限。
//
// 同时递增 token_version：已签发令牌在 state_ttl（≤60s）内失效，
// 避免「旧 secret 泄露但旧令牌仍可用」的窗口（§21.2.3）。
func (s *M2MService) RotateSecret(ctx context.Context, clientID, actorID, traceID string) (*RotatedM2MSecret, error) {
	client, err := s.loadClient(ctx, clientID)
	if err != nil {
		return nil, err
	}
	if !client.Active() {
		return nil, apperr.Conflict("m2m client is not active")
	}
	now := s.clock.Now().UTC()
	secret, err := generateM2MSecret()
	if err != nil {
		return nil, err
	}
	hash := HashM2MSecret(secret)
	id, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate secret id failed", err)
	}
	record := &domain.M2MSecret{
		ID:          id,
		ClientID:    client.ClientID,
		SecretHash:  hash,
		Fingerprint: M2MSecretFingerprint(hash),
		Status:      domain.M2MSecretCurrent,
		CreatedAt:   now,
	}
	expiresAt := now.Add(s.rotateGrace)
	// 先退役旧密钥，再写入新密钥：顺序保证任何时刻最多 1 个 current。
	existing, err := s.secrets.ListByClient(ctx, client.ClientID)
	if err != nil {
		return nil, err
	}
	for _, item := range existing {
		if item.Status != domain.M2MSecretCurrent {
			continue
		}
		retired := *item
		retired.Status = domain.M2MSecretRetiring
		if s.rotateGrace > 0 {
			retired.ExpiresAt = &expiresAt
		}
		if err := s.secrets.UpdateStatus(ctx, &retired); err != nil {
			return nil, err
		}
	}
	if err := s.secrets.Save(ctx, record); err != nil {
		return nil, err
	}
	client.TokenVersion++
	client.RotationDueAt = &now
	client.UpdatedAt = now
	if err := s.clients.Save(ctx, client, client.Version); err != nil {
		return nil, err
	}
	s.invalidateClientState(ctx, client.ClientID)
	s.emitAudit(ctx, audit.ActionAuthM2MSecretRotated, actorID, traceID, client.ClientID,
		audit.OutcomeSuccess, "", map[string]string{"fingerprint": record.Fingerprint})
	result := &RotatedM2MSecret{ClientID: client.ClientID, ClientSecret: secret, Fingerprint: record.Fingerprint}
	if s.rotateGrace > 0 {
		result.ExpiresAt = expiresAt
	}
	return result, nil
}

// DisableClient 停用客户端：递增 token_version、吊销全部密钥。
func (s *M2MService) DisableClient(ctx context.Context, clientID, actorID, traceID string) error {
	return s.setClientStatus(ctx, clientID, domain.M2MClientDisabled, audit.ActionAuthM2MClientDisabled, actorID, traceID)
}

// EnableClient 启用客户端；不复活旧令牌与旧密钥。
func (s *M2MService) EnableClient(ctx context.Context, clientID, actorID, traceID string) error {
	client, err := s.loadClient(ctx, clientID)
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	client.Status = domain.M2MClientActive
	client.TokenVersion++
	client.UpdatedAt = now
	client.DeletedAt = nil
	if err := s.clients.Save(ctx, client, client.Version); err != nil {
		return err
	}
	s.invalidateClientState(ctx, client.ClientID)
	s.emitAudit(ctx, audit.ActionAuthM2MClientEnabled, actorID, traceID, client.ClientID,
		audit.OutcomeSuccess, "", nil)
	return nil
}

// DeleteClient 软删客户端；client_id 不可复用，审计与指纹保留。
func (s *M2MService) DeleteClient(ctx context.Context, clientID, actorID, traceID string) error {
	client, err := s.loadClient(ctx, clientID)
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	client.Status = domain.M2MClientDeleted
	client.TokenVersion++
	client.UpdatedAt = now
	client.DeletedAt = &now
	if err := s.clients.Save(ctx, client, client.Version); err != nil {
		return err
	}
	if _, err := s.secrets.RevokeAll(ctx, client.ClientID, now); err != nil {
		return err
	}
	s.invalidateClientState(ctx, client.ClientID)
	s.emitAudit(ctx, audit.ActionAuthM2MClientDeleted, actorID, traceID, client.ClientID,
		audit.OutcomeSuccess, "", nil)
	return nil
}

// IssueToken 校验客户端凭据并签发机器令牌，同时上报签发指标。
func (s *M2MService) IssueToken(ctx context.Context, input IssueTokenInput) (*M2MToken, error) {
	start := s.clock.Now()
	token, err := s.issueToken(ctx, input)
	if s.observer != nil {
		result, reason := "issued", ""
		if err != nil {
			result = "denied"
			reason = m2mDeniedReason(err)
		}
		s.observer.ObserveM2MToken(ctx, input.ClientID, string(s.channel), result, reason, s.clock.Now().Sub(start))
	}
	return token, err
}

// m2mDeniedReason 把用例错误映射为低基数原因标签。
func m2mDeniedReason(err error) string {
	switch {
	case errors.Is(err, ErrM2MInvalidClient):
		return "invalid_client"
	case errors.Is(err, ErrM2MInvalidScope):
		return "invalid_scope"
	default:
		return "error"
	}
}

// observeSecretAge 上报本次认证所用 secret 的年龄；超 max_secret_age 由告警规则判定。
func (s *M2MService) observeSecretAge(ctx context.Context, clientID string, record *domain.M2MSecret, now time.Time) {
	if s.observer == nil || record == nil || record.CreatedAt.IsZero() {
		return
	}
	s.observer.ObserveM2MSecretAge(ctx, clientID, now.Sub(record.CreatedAt))
}

// issueToken 是签发实现主体：失败原因对外一律 invalid_client
// （不区分不存在 / 停用 / 密钥错误），审计中保留真实原因供排障。
func (s *M2MService) issueToken(ctx context.Context, input IssueTokenInput) (*M2MToken, error) {
	clientID := strings.TrimSpace(input.ClientID)
	secret := strings.TrimSpace(input.ClientSecret)
	if clientID == "" || secret == "" {
		s.auditDenied(ctx, clientID, input.TraceID, "invalid_client")
		return nil, ErrM2MInvalidClient
	}
	client, err := s.clients.FindByClientID(ctx, clientID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// 假比对：让「不存在」与「密钥错误」耗时一致。
			compareM2MSecret(m2mDummySecretHash, secret)
			s.auditDenied(ctx, clientID, input.TraceID, "invalid_client")
			return nil, ErrM2MInvalidClient
		}
		return nil, err
	}
	if !client.Active() {
		s.auditDenied(ctx, client.ClientID, input.TraceID, "disabled")
		return nil, ErrM2MInvalidClient
	}
	hash := HashM2MSecret(secret)
	record, err := s.secrets.FindByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			compareM2MSecret(m2mDummySecretHash, secret)
			s.auditDenied(ctx, client.ClientID, input.TraceID, "invalid_client")
			return nil, ErrM2MInvalidClient
		}
		return nil, err
	}
	now := s.clock.Now().UTC()
	if record.ClientID != client.ClientID || !record.Usable(now) || !compareM2MSecret(record.SecretHash, secret) {
		s.auditDenied(ctx, client.ClientID, input.TraceID, "invalid_client")
		return nil, ErrM2MInvalidClient
	}
	s.observeSecretAge(ctx, client.ClientID, record, now)
	if !client.AllowsChannel(s.channel) {
		s.auditDenied(ctx, client.ClientID, input.TraceID, "invalid_scope")
		return nil, ErrM2MInvalidScope
	}
	scopes := strings.Fields(input.Scope)
	if len(scopes) == 0 {
		s.auditDenied(ctx, client.ClientID, input.TraceID, "invalid_scope")
		return nil, ErrM2MInvalidScope
	}
	for _, scope := range scopes {
		if !client.AllowsScope(scope) {
			s.auditDenied(ctx, client.ClientID, input.TraceID, "invalid_scope")
			return nil, ErrM2MInvalidScope
		}
		if _, ok := s.allowedScope[scope]; !ok {
			s.auditDenied(ctx, client.ClientID, input.TraceID, "invalid_scope")
			return nil, ErrM2MInvalidScope
		}
	}
	token, expiresIn, err := s.tokens.IssueClientToken(client.ClientID, client.TokenVersion, scopes)
	if err != nil {
		return nil, err
	}
	s.emitAudit(ctx, audit.ActionAuthM2MTokenIssued, systemActor, input.TraceID, client.ClientID,
		audit.OutcomeSuccess, "", map[string]string{
			"channel": string(s.channel),
			"scope":   strings.Join(scopes, " "),
		})
	return &M2MToken{AccessToken: token, ExpiresIn: expiresIn, Scopes: scopes}, nil
}

// systemActor 是机器主体在审计中的 actor_id 占位；调用方传该值表示无人工主体。
const systemActor = ""

// m2mActorID 返回机器事件的 actor_id：优先 client_id，缺失时用固定占位。
func m2mActorID(clientID string) string {
	if trimmed := strings.TrimSpace(clientID); trimmed != "" {
		return trimmed
	}
	return "m2m:unknown"
}

// setClientStatus 统一处理停用类状态迁移（停用 / 软删之外的处置）。
func (s *M2MService) setClientStatus(
	ctx context.Context,
	clientID string,
	status domain.M2MClientStatus,
	action, actorID, traceID string,
) error {
	client, err := s.loadClient(ctx, clientID)
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	client.Status = status
	client.TokenVersion++
	client.UpdatedAt = now
	if err := s.clients.Save(ctx, client, client.Version); err != nil {
		return err
	}
	if _, err := s.secrets.RevokeAll(ctx, client.ClientID, now); err != nil {
		return err
	}
	s.invalidateClientState(ctx, client.ClientID)
	s.emitAudit(ctx, action, actorID, traceID, client.ClientID, audit.OutcomeSuccess, "", nil)
	return nil
}

// loadClient 按对外标识加载客户端并转换为统一错误。
func (s *M2MService) loadClient(ctx context.Context, clientID string) (*domain.M2MClient, error) {
	trimmed := strings.TrimSpace(clientID)
	if trimmed == "" {
		return nil, apperr.BadRequest("client_id is required")
	}
	return s.clients.FindByClientID(ctx, trimmed)
}

// checkScopes 校验申请 scope 均命中全局白名单。
func (s *M2MService) checkScopes(scopes []string) error {
	for _, scope := range scopes {
		if _, ok := s.allowedScope[scope]; !ok {
			return apperr.BadRequest("m2m scope is not allowed: " + scope)
		}
	}
	return nil
}

// invalidateClientState 使客户端撤销快照失效；失败只告警，不改变已提交结果。
func (s *M2MService) invalidateClientState(ctx context.Context, clientID string) {
	if s.clientStates == nil {
		return
	}
	if err := s.clientStates.DeleteClientState(ctx, clientID); err != nil && s.logger != nil {
		s.logger.Warn("identity m2m client snapshot invalidate failed",
			"client_id", clientID, "error", err.Error())
	}
}

// softDeleteOrphan 清理「客户端已建、密钥未建成」的孤儿客户端。
func (s *M2MService) softDeleteOrphan(ctx context.Context, client *domain.M2MClient, _, traceID string) {
	now := s.clock.Now().UTC()
	client.Status = domain.M2MClientDeleted
	client.DeletedAt = &now
	client.UpdatedAt = now
	client.TokenVersion++
	if err := s.clients.Save(ctx, client, client.Version); err != nil && s.logger != nil {
		s.logger.Error("identity m2m orphan client cleanup failed",
			"client_id", client.ClientID, "error", err.Error())
	}
	if s.logger != nil {
		s.logger.Warn("identity m2m client created without secret", "client_id", client.ClientID, "trace_id", traceID)
	}
}

// emitAudit 写入 M2M 审计事件；失败不改变业务结果，但记录告警。
func (s *M2MService) emitAudit(
	ctx context.Context,
	action, actorID, traceID, clientID string,
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
			s.logger.Error("identity m2m audit event id failed", "action", action, "error", err.Error())
		}
		return
	}
	actorType := "system"
	if actorID != systemActor {
		actorType = "staff"
	} else {
		// 机器事件没有人工主体，但 audit.Event 要求 actor_id 非空：
		// 用 client_id 标识来源，缺失时落到固定占位，避免整条审计被校验拒绝后丢弃。
		actorID = m2mActorID(clientID)
	}
	event := audit.Event{
		EventID:      eventID,
		Action:       action,
		ActorID:      actorID,
		ActorType:    actorType,
		Channel:      string(s.channel),
		ResourceType: "m2m_client",
		ResourceID:   clientID,
		Outcome:      outcome,
		OccurredAt:   s.clock.Now().UTC(),
		TraceID:      normalizeTraceID(traceID),
		Service:      s.service,
		Reason:       reason,
		Attrs:        attrs,
	}
	if err := s.audit.Emit(ctx, event); err != nil && s.logger != nil {
		s.logger.Error("identity m2m audit emit failed", "action", action, "error", err.Error())
	}
}

// auditDenied 记录签发失败事件。
func (s *M2MService) auditDenied(ctx context.Context, clientID, traceID, reason string) {
	s.emitAudit(ctx, audit.ActionAuthM2MTokenDenied, systemActor, traceID, clientID,
		audit.OutcomeFailure, reason, map[string]string{"channel": string(s.channel)})
}

// generateM2MClientID 生成 svc_<16 字节 base64url>。
func generateM2MClientID() (string, error) {
	buf := make([]byte, m2mClientIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", apperr.Internal("generate m2m client id failed", err)
	}
	return m2mClientIDPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// generateM2MSecret 生成 32 字节 base64url 密钥。
func generateM2MSecret() (string, error) {
	buf := make([]byte, m2mSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", apperr.Internal("generate m2m secret failed", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashM2MSecret 返回密钥的 SHA-256 十六进制摘要；库内只存该值。
func HashM2MSecret(secret string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(secret)))
	return hex.EncodeToString(sum[:])
}

// M2MSecretFingerprint 返回展示用指纹；长度不足以反推明文。
func M2MSecretFingerprint(hash string) string {
	if len(hash) < m2mFingerprintHexLen {
		return m2mFingerprintPrefix + hash
	}
	return m2mFingerprintPrefix + hash[:m2mFingerprintHexLen]
}

// compareM2MSecret 以常数时间比较摘要与明文密钥，返回是否匹配。
func compareM2MSecret(expectedHash, secret string) bool {
	computed := HashM2MSecret(secret)
	return subtle.ConstantTimeCompare([]byte(expectedHash), []byte(computed)) == 1
}
