package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// ChangeRequestService 是高风险 IAM 变更的审批用例。
//
// 职责边界：
//   - 本服务只维护审批单自身状态（创建 / 列表 / 通过 / 拒绝 / 撤销 / 标记应用）；
//   - 真正的目标变更由 ChangeApplicator 执行，执行时**重新加载**目标聚合、
//     重新校验权限与版本，不得直接信任载荷（载荷可能在审批期间已过期）；
//   - 目标变更的审计由 RoleService / BindingService 产生（action=rbac.role_change /
//     rbac.binding_change），本服务不重复写审计，避免同一次变更出现两条事实。
type ChangeRequestService struct {
	requests   domain.ChangeRequestRepository
	applicator ChangeApplicator
	revoker    BreakglassRevoker
	uow        UnitOfWork
	log        *slog.Logger
	ids        id.Generator
	limits     ChangeLimits
}

// ChangeLimits 是审批流的安全开关与规模上限。
type ChangeLimits struct {
	// RequireReason 创建审批单时是否强制填写原因。
	RequireReason bool
	// AllowBizChannel 是否允许 B 端发起与决定审批单。
	//
	// 依赖 iam_change_request.merchant_id（迁移 000005）做租户隔离：
	// 关闭时 B 端创建直接返回 40300，决定类接口返回 40400。
	AllowBizChannel bool
	// AllowBreakglass 是否允许 breakglass 动作；破玻璃流程属 M4.3。
	AllowBreakglass bool
}

// ChangeScope 是一次审批操作的数据作用域，由 transport 从 Principal 派生。
//
// 服务层不信任调用方自报的端别与商户：端别来自路由挂载点，商户来自认证主体。
// 二者共同决定「哪些审批单可见」，缺一即按不可见处理（fail-closed），
// 避免某个入口忘记带商户时退化成跨租户可读（SEC-14）。
type ChangeScope struct {
	// Channel 请求所在端别。
	Channel domain.Channel
	// MerchantID 商户 ID；A 端为空，B 端非空。
	MerchantID string
}

// Validate 校验作用域自洽：A 端不得携带商户，B 端必须携带商户。
func (s ChangeScope) Validate() error {
	if err := s.Channel.Validate(); err != nil {
		return err
	}
	switch s.Channel {
	case domain.ChannelBiz:
		if strings.TrimSpace(s.MerchantID) == "" {
			return apperr.Forbidden("merchant id is required")
		}
	case domain.ChannelAdmin:
		if s.MerchantID != "" {
			return apperr.BadRequest("merchant id is not allowed on channel admin")
		}
	}
	return nil
}

// VisibleTo 报告审批单是否落在作用域内。
func (s ChangeScope) VisibleTo(request *domain.ChangeRequest) bool {
	if request == nil {
		return false
	}
	return request.VisibleTo(s.Channel, s.MerchantID)
}

// ChangeRequestServiceConfig 是 ChangeRequestService 的装配参数。
type ChangeRequestServiceConfig struct {
	Requests   domain.ChangeRequestRepository
	Applicator ChangeApplicator
	// Revoker 撤销破玻璃绑定；为空时尝试从 Applicator 推导。
	Revoker BreakglassRevoker
	// UnitOfWork 让「目标聚合写入 + 审批单状态 + 审计」同事务提交。
	UnitOfWork UnitOfWork
	IDs        id.Generator
	Limits     ChangeLimits
	Logger     *slog.Logger
}

// NewChangeRequestService 构造审批用例；缺失必要依赖时返回错误（fail-closed）。
func NewChangeRequestService(cfg ChangeRequestServiceConfig) (*ChangeRequestService, error) {
	if cfg.Requests == nil {
		return nil, apperr.Internal("iam change request repository is required", nil)
	}
	if cfg.Applicator == nil {
		return nil, apperr.Internal("iam change applicator is required", nil)
	}
	if cfg.IDs == nil {
		cfg.IDs = id.MustSnowflake(id.Options{NodeID: 1})
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	revoker := cfg.Revoker
	if revoker == nil {
		if candidate, ok := cfg.Applicator.(BreakglassRevoker); ok {
			revoker = candidate
		}
	}
	uow := cfg.UnitOfWork
	if uow == nil {
		uow = NoopUnitOfWork{}
	}
	return &ChangeRequestService{
		requests:   cfg.Requests,
		applicator: cfg.Applicator,
		revoker:    revoker,
		uow:        uow,
		log:        logger,
		ids:        cfg.IDs,
		limits:     cfg.Limits,
	}, nil
}

// CreateChangeRequestCommand 是创建审批单的入参。
type CreateChangeRequestCommand struct {
	Channel domain.Channel
	// MerchantID 商户 ID：B 端必填（由 Principal 派生），A 端必须为空。
	MerchantID string
	Action     domain.ChangeAction
	TargetID   string
	Payload    []byte
	Reason     string
	Actor      string
}

// Create 创建待审批单。
//
// 创建本身不改变任何授权，因此不写审计；审批通过后由 applicator 产生真实变更审计。
func (s *ChangeRequestService) Create(ctx context.Context, cmd CreateChangeRequestCommand) (*domain.ChangeRequest, error) {
	if strings.TrimSpace(cmd.Actor) == "" {
		return nil, apperr.BadRequest("actor is required")
	}
	scope := ChangeScope{Channel: cmd.Channel, MerchantID: cmd.MerchantID}
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if s.limits.RequireReason && strings.TrimSpace(cmd.Reason) == "" {
		return nil, apperr.BadRequest("reason is required")
	}
	if cmd.Channel == domain.ChannelBiz {
		// B 端审批流随 000005 落地；关闭时按不可用处理，而不是静默降级为 A 端单据。
		if !s.limits.AllowBizChannel {
			return nil, apperr.Forbidden("biz channel change requests are not enabled")
		}
		// 破玻璃是平台级应急能力：允许商户自助申请等于把超级权限的入口下放到租户，
		// 超出 M4 的威胁模型，B 端一律拒绝。
		if cmd.Action == domain.ChangeActionBreakglassRequest {
			return nil, apperr.Forbidden("breakglass is not available on channel biz")
		}
	}
	if cmd.Action == domain.ChangeActionBreakglassRequest {
		if !s.limits.AllowBreakglass {
			return nil, apperr.Forbidden("breakglass is not enabled")
		}
		if err := validateBreakglassRequest(cmd.Channel, cmd.Payload); err != nil {
			return nil, err
		}
	}
	requestID, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate change request id", err)
	}
	request, err := domain.NewChangeRequest(domain.NewChangeRequestParams{
		ID:         requestID,
		Channel:    cmd.Channel,
		MerchantID: cmd.MerchantID,
		Action:     cmd.Action,
		TargetID:   cmd.TargetID,
		Payload:    cmd.Payload,
		Reason:     cmd.Reason,
		Actor:      cmd.Actor,
	})
	if err != nil {
		return nil, err
	}
	if err := s.requests.Save(ctx, request, 0); err != nil {
		return nil, err
	}
	return request, nil
}

// Get 按 ID 加载审批单；跨端 / 跨商户一律 404（SEC-14）。
func (s *ChangeRequestService) Get(ctx context.Context, requestID string, scope ChangeScope) (*domain.ChangeRequest, error) {
	return s.loadScoped(ctx, requestID, scope)
}

// List 分页列举审批单；端别与商户强制取自作用域，调用方无法越界查询。
//
// Limit 上限 100（API-05）。A 端 merchantID 为空，SQL 侧只按 channel 过滤，
// 由端别的不变量保证不会混入商户单据；B 端追加 merchant_id 条件。
func (s *ChangeRequestService) List(
	ctx context.Context,
	scope ChangeScope,
	filter domain.ChangeRequestFilter,
) ([]*domain.ChangeRequest, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	filter.Channel = scope.Channel
	filter.MerchantID = scope.MerchantID
	if err := validateChangePage(&filter); err != nil {
		return nil, err
	}
	return s.requests.List(ctx, filter)
}

// Count 返回与 List 相同条件的审批单总数，供分页 total 使用。
func (s *ChangeRequestService) Count(
	ctx context.Context,
	scope ChangeScope,
	filter domain.ChangeRequestFilter,
) (int, error) {
	if err := scope.Validate(); err != nil {
		return 0, err
	}
	filter.Channel = scope.Channel
	filter.MerchantID = scope.MerchantID
	if err := validateChangePage(&filter); err != nil {
		return 0, err
	}
	return s.requests.Count(ctx, filter)
}

// loadScoped 按作用域加载审批单：不存在、跨端、跨商户统一返回 40400。
//
// 40300 会泄露「该 ID 确实存在」这一事实，因此越权访问一律伪装成不存在。
func (s *ChangeRequestService) loadScoped(
	ctx context.Context,
	requestID string,
	scope ChangeScope,
) (*domain.ChangeRequest, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	request, err := s.requests.FindByID(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if !scope.VisibleTo(request) {
		return nil, apperr.NotFound("change request not found")
	}
	return request, nil
}

// Approve 通过审批单；审批人必须与申请人不同（SoD）。
func (s *ChangeRequestService) Approve(
	ctx context.Context,
	requestID string,
	version int64,
	approver string,
	scope ChangeScope,
) (*domain.ChangeRequest, error) {
	request, err := s.loadForDecision(ctx, requestID, version, scope)
	if err != nil {
		return nil, err
	}
	if err := request.Approve(approver, version); err != nil {
		return nil, err
	}
	if err := s.requests.Save(ctx, request, version); err != nil {
		return nil, err
	}
	return request, nil
}

// Reject 拒绝审批单；同样适用职责分离。
func (s *ChangeRequestService) Reject(
	ctx context.Context,
	requestID string,
	version int64,
	approver string,
	scope ChangeScope,
) (*domain.ChangeRequest, error) {
	request, err := s.loadForDecision(ctx, requestID, version, scope)
	if err != nil {
		return nil, err
	}
	if err := request.Reject(approver, version); err != nil {
		return nil, err
	}
	if err := s.requests.Save(ctx, request, version); err != nil {
		return nil, err
	}
	return request, nil
}

// Cancel 由申请人撤销待审批单。
func (s *ChangeRequestService) Cancel(
	ctx context.Context,
	requestID string,
	version int64,
	actor string,
	scope ChangeScope,
) (*domain.ChangeRequest, error) {
	request, err := s.loadForDecision(ctx, requestID, version, scope)
	if err != nil {
		return nil, err
	}
	if err := request.Cancel(actor, version); err != nil {
		return nil, err
	}
	if err := s.requests.Save(ctx, request, version); err != nil {
		return nil, err
	}
	return request, nil
}

// loadForDecision 按作用域加载审批单并校验乐观锁，避免用旧页面覆盖他人决定。
func (s *ChangeRequestService) loadForDecision(
	ctx context.Context,
	requestID string,
	version int64,
	scope ChangeScope,
) (*domain.ChangeRequest, error) {
	request, err := s.loadScoped(ctx, requestID, scope)
	if err != nil {
		return nil, err
	}
	if request.Version != version {
		return nil, apperr.Conflict("change request version conflict")
	}
	return request, nil
}

// ApplyChangeRequestCommand 是应用审批单的入参。
type ApplyChangeRequestCommand struct {
	ID      string
	Version int64
	Actor   string
	Scope   ChangeScope
}

// Apply 执行已通过审批单。
//
// 失败时审批单保持 approved（可重试），不会变成 applied：宁可让人工重试，
// 也不能出现「标记完成但目标未变更」的审计黑洞。
func (s *ChangeRequestService) Apply(ctx context.Context, cmd ApplyChangeRequestCommand) (*domain.ChangeRequest, error) {
	if err := cmd.Scope.Validate(); err != nil {
		return nil, err
	}
	request, err := s.loadScoped(ctx, cmd.ID, cmd.Scope)
	if err != nil {
		return nil, err
	}
	if request.Version != cmd.Version {
		return nil, apperr.Conflict("change request version conflict")
	}
	if !request.IsApproved() {
		return nil, apperr.Conflict("change request is not approved")
	}
	actor := strings.TrimSpace(cmd.Actor)
	if actor == "" {
		actor = request.ApprovedBy
	}
	if actor == "" {
		return nil, apperr.BadRequest("actor is required")
	}

	// 目标变更与审批单状态必须同事务：否则可能出现「权限已生效但审批单仍是 approved」
	// （重复应用 → 重复审计）或反之。契约由 UnitOfWork 保证，嵌套调用复用外层事务。
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		targetID, applyErr := s.applicator.Apply(ctx, request, actor)
		if applyErr != nil {
			return applyErr
		}
		if targetID != "" && request.TargetID == "" {
			if err := request.BindTarget(targetID); err != nil {
				return err
			}
		}
		expected := request.Version
		if err := request.MarkApplied(expected); err != nil {
			return err
		}
		return s.requests.Save(ctx, request, expected)
	}); err != nil {
		// 契约冲突（版本过期 / 状态不符 / 目标已变更）是预期内的可重试结果，
		// 只记 Warn 一次；其余错误同样返回，交由上层映射 50000 / 40900。
		s.log.WarnContext(ctx, "iam change request apply failed",
			"change_request_id", request.ID, "action", string(request.Action), "err", err)
		return nil, err
	}
	return request, nil
}

// RevokeBreakglass 撤销已生效的破玻璃绑定（提前收回超级权限）。
//
// 破玻璃是平台级能力（B 端创建已被 Create 拒绝），因此这里只接受 A 端审批单，
// 不引入商户参数；若未来开放 B 端，必须与 Create 同步补作用域校验。
// 审批单保持 applied 事实不变：撤销是一次新的授权变更，由 binding 审计记录，
// 不篡改历史审批状态。
func (s *ChangeRequestService) RevokeBreakglass(ctx context.Context, requestID, actor string) error {
	if strings.TrimSpace(actor) == "" {
		return apperr.BadRequest("actor is required")
	}
	request, err := s.requests.FindByID(ctx, requestID)
	if err != nil {
		return err
	}
	if request.Channel != domain.ChannelAdmin {
		return apperr.NotFound("change request not found")
	}
	if request.Action != domain.ChangeActionBreakglassRequest {
		return apperr.BadRequest("change request is not a breakglass request")
	}
	if request.Status != domain.ChangeStatusApplied || request.TargetID == "" {
		return apperr.Conflict("breakglass request is not applied")
	}
	if s.revoker == nil {
		return apperr.Internal("breakglass revoker is not configured", nil)
	}
	reason := strings.TrimSpace(request.Reason)
	if reason == "" {
		reason = "revoke breakglass " + request.ID
	}
	return s.revoker.RevokeBreakglass(ctx, request.TargetID, actor, reason)
}

// BreakglassRevoker 撤销破玻璃绑定；由 RoleChangeApplicator 实现。
type BreakglassRevoker interface {
	RevokeBreakglass(ctx context.Context, bindingID, actor, reason string) error
}

// validateChangePage 复用分页上限校验（API-05）。
func validateChangePage(filter *domain.ChangeRequestFilter) error {
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	if filter.Limit > 100 {
		return apperr.BadRequest("page_size exceeds maximum of 100")
	}
	return nil
}

// ChangeApplicator 执行审批通过后的目标变更。
//
// 实现必须：
//   - 按 request.TargetID 重新加载目标聚合并按当前 version 写入（不得信任载荷里的版本）；
//   - 幂等安全：同一审批单重复调用要么成功且结果一致，要么返回明确错误；
//   - 返回新建聚合的真实 ID（仅新建类动作需要），由用例回填到审批单。
type ChangeApplicator interface {
	Apply(ctx context.Context, request *domain.ChangeRequest, actor string) (targetID string, err error)
}

// RoleChangeApplicator 用 RoleService / BindingService 执行角色与绑定类变更。
type RoleChangeApplicator struct {
	roles    *RoleService
	bindings *BindingService
}

// NewRoleChangeApplicator 构造角色 / 绑定审批执行器。
func NewRoleChangeApplicator(roles *RoleService, bindings *BindingService) (*RoleChangeApplicator, error) {
	if roles == nil || bindings == nil {
		return nil, apperr.Internal("iam role and binding services are required for approval", nil)
	}
	return &RoleChangeApplicator{roles: roles, bindings: bindings}, nil
}

// roleCreatePayload 是 role_create 动作的载荷。
type roleCreatePayload struct {
	Scope       string             `json:"scope_type"`
	Code        string             `json:"code"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Permissions []string           `json:"permissions"`
	DataScopes  []dataScopePayload `json:"data_scopes"`
}

// roleUpdatePayload 是 role_update 动作的载荷；nil 表示不修改。
type roleUpdatePayload struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
}

// rolePermissionReplacePayload 是 role_permission_replace 动作的载荷。
type rolePermissionReplacePayload struct {
	Permissions []string            `json:"permissions"`
	DataScopes  *[]dataScopePayload `json:"data_scopes"`
}

// roleClonePayload 是 role_clone 动作的载荷。
type roleClonePayload struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// breakglassRequestPayload 是 breakglass_request 动作的载荷。
//
// 只表达「给谁、多久」，不表达权限点：超级权限由角色的 "*" 在有效期内展开。
type breakglassRequestPayload struct {
	SubjectType     string `json:"subject_type"`
	SubjectID       string `json:"subject_id"`
	MerchantID      string `json:"merchant_id"`
	DurationSeconds int    `json:"duration_seconds"`
}

// validateBreakglassRequest 在创建阶段严格校验载荷，避免无效申请占用审批人精力。
func validateBreakglassRequest(channel domain.Channel, payload []byte) error {
	var decoded breakglassRequestPayload
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return apperr.BadRequest("breakglass payload is invalid: " + err.Error())
	}
	if err := decoder.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		return apperr.BadRequest("breakglass payload must contain exactly one JSON object")
	}
	_, err := domain.NewBreakglassGrant(domain.NewBreakglassGrantParams{
		Channel:     channel,
		SubjectType: domain.SubjectType(decoded.SubjectType),
		SubjectID:   decoded.SubjectID,
		MerchantID:  decoded.MerchantID,
		Duration:    time.Duration(decoded.DurationSeconds) * time.Second,
	})
	return err
}

// bindingCreatePayload 是 binding_create 动作的载荷。
type bindingCreatePayload struct {
	RoleID      string     `json:"role_id"`
	SubjectType string     `json:"subject_type"`
	SubjectID   string     `json:"subject_id"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

// dataScopePayload 是数据范围的传输形态（与角色 DTO 的 scope_type 字段保持一致）。
type dataScopePayload struct {
	Resource    string   `json:"resource"`
	Type        string   `json:"scope_type"`
	MerchantIDs []string `json:"merchant_ids"`
}

// Apply 按动作分发到对应用例。
//
// 每个分支都重新加载目标聚合：审批期间角色可能已被停用或改版，
// 直接按载荷里的 version 写入会造成越权或覆盖他人变更。
func (a *RoleChangeApplicator) Apply(ctx context.Context, request *domain.ChangeRequest, actor string) (string, error) {
	switch request.Action {
	case domain.ChangeActionRoleCreate:
		return a.applyRoleCreate(ctx, request, actor)
	case domain.ChangeActionRoleClone:
		return a.applyRoleClone(ctx, request, actor)
	case domain.ChangeActionBindingCreate:
		return a.applyBindingCreate(ctx, request, actor)
	case domain.ChangeActionRoleUpdate:
		return "", a.applyRoleUpdate(ctx, request, actor)
	case domain.ChangeActionRolePermissionReplace:
		return "", a.applyRolePermissionReplace(ctx, request, actor)
	case domain.ChangeActionRoleDelete:
		return "", a.applyRoleDelete(ctx, request, actor)
	case domain.ChangeActionBindingDelete:
		return "", a.applyBindingDelete(ctx, request, actor)
	case domain.ChangeActionBreakglassRequest:
		return a.applyBreakglassRequest(ctx, request, actor)
	default:
		return "", apperr.Internal("unsupported change action "+string(request.Action), nil)
	}
}

// applyRoleCreate 创建角色并返回新角色 ID。
func (a *RoleChangeApplicator) applyRoleCreate(ctx context.Context, request *domain.ChangeRequest, actor string) (string, error) {
	var payload roleCreatePayload
	if err := decodeChangePayload(request, &payload); err != nil {
		return "", err
	}
	scope := domain.RoleScopeType(payload.Scope)
	if request.Channel == domain.ChannelBiz {
		// B 端只能创建本商户角色：载荷里的 scope_type 最多只能「确认」merchant，
		// 不允许用它把单据提升为 platform 角色（那是 A 端专属能力）。
		if scope != "" && scope != domain.RoleScopeMerchant {
			return "", apperr.BadRequest("channel biz may only create merchant roles")
		}
		scope = domain.RoleScopeMerchant
	} else if scope == "" {
		scope = domain.RoleScopePlatform
	}
	permissions, err := permissionCodesFromStrings(payload.Permissions)
	if err != nil {
		return "", err
	}
	scopes, err := dataScopesFromPayload(payload.DataScopes)
	if err != nil {
		return "", err
	}
	role, err := a.roles.CreateRole(ctx, CreateRoleCommand{
		Channel:     request.Channel,
		Scope:       scope,
		MerchantID:  request.MerchantID,
		Code:        payload.Code,
		Name:        payload.Name,
		Description: payload.Description,
		Permissions: permissions,
		DataScopes:  scopes,
		Actor:       actor,
	})
	if err != nil {
		return "", err
	}
	return role.ID, nil
}

// loadRoleForChange 按审批单作用域加载目标角色。
//
// 仓储只按 (id, channel) 定位，因此必须再比对 merchant_id：否则 B 端可以拿
// 一张本商户的审批单，把 target_id 指向其它商户的角色 ID 来完成越权变更（SEC-14）。
// 跨商户统一返回 404，不泄露角色是否存在。
func (a *RoleChangeApplicator) loadRoleForChange(ctx context.Context, request *domain.ChangeRequest) (*domain.Role, error) {
	role, err := a.roles.LoadForChange(ctx, request.TargetID, request.Channel)
	if err != nil {
		return nil, err
	}
	if role.MerchantID != request.MerchantID {
		return nil, apperr.NotFound("role not found")
	}
	return role, nil
}

// applyRoleUpdate 应用名称 / 说明 / 启停变更。
func (a *RoleChangeApplicator) applyRoleUpdate(ctx context.Context, request *domain.ChangeRequest, actor string) error {
	var payload roleUpdatePayload
	if err := decodeChangePayload(request, &payload); err != nil {
		return err
	}
	role, err := a.loadRoleForChange(ctx, request)
	if err != nil {
		return err
	}
	_, err = a.roles.UpdateRole(ctx, UpdateRoleCommand{
		ID:          role.ID,
		Name:        payload.Name,
		Description: payload.Description,
		Status:      payload.Status,
		Version:     role.Version,
		Actor:       actor,
		Reason:      request.Reason,
	})
	return err
}

// applyRolePermissionReplace 全量替换角色权限与数据范围。
func (a *RoleChangeApplicator) applyRolePermissionReplace(ctx context.Context, request *domain.ChangeRequest, actor string) error {
	var payload rolePermissionReplacePayload
	if err := decodeChangePayload(request, &payload); err != nil {
		return err
	}
	permissions, err := permissionCodesFromStrings(payload.Permissions)
	if err != nil {
		return err
	}
	role, err := a.loadRoleForChange(ctx, request)
	if err != nil {
		return err
	}
	var scopes *[]domain.DataScope
	if payload.DataScopes != nil {
		converted, convertErr := dataScopesFromPayload(*payload.DataScopes)
		if convertErr != nil {
			return convertErr
		}
		scopes = &converted
	}
	_, _, err = a.roles.ReplacePermissions(ctx, ReplacePermissionsCommand{
		ID:          role.ID,
		Permissions: permissions,
		DataScopes:  scopes,
		Version:     role.Version,
		Actor:       actor,
		Reason:      request.Reason,
	})
	return err
}

// applyRoleClone 复制角色并返回新角色 ID。
func (a *RoleChangeApplicator) applyRoleClone(ctx context.Context, request *domain.ChangeRequest, actor string) (string, error) {
	var payload roleClonePayload
	if err := decodeChangePayload(request, &payload); err != nil {
		return "", err
	}
	// 先按作用域加载源角色：克隆会把源角色的权限与数据范围整份复制，
	// 跨商户克隆等于直接照抄别人的授权（SEC-14）。
	source, err := a.loadRoleForChange(ctx, request)
	if err != nil {
		return "", err
	}
	role, err := a.roles.CloneRole(ctx, CloneRoleCommand{
		SourceID: source.ID,
		Code:     payload.Code,
		Name:     payload.Name,
		Actor:    actor,
	})
	if err != nil {
		return "", err
	}
	return role.ID, nil
}

// applyRoleDelete 软删角色。
func (a *RoleChangeApplicator) applyRoleDelete(ctx context.Context, request *domain.ChangeRequest, actor string) error {
	role, err := a.loadRoleForChange(ctx, request)
	if err != nil {
		return err
	}
	return a.roles.DeleteRole(ctx, DeleteRoleCommand{
		ID:      role.ID,
		Version: role.Version,
		Actor:   actor,
		Reason:  request.Reason,
	})
}

// applyBindingCreate 绑定角色并返回新绑定 ID。
func (a *RoleChangeApplicator) applyBindingCreate(ctx context.Context, request *domain.ChangeRequest, actor string) (string, error) {
	var payload bindingCreatePayload
	if err := decodeChangePayload(request, &payload); err != nil {
		return "", err
	}
	binding, err := a.bindings.Bind(ctx, BindCommand{
		Channel:     request.Channel,
		SubjectType: domain.SubjectType(payload.SubjectType),
		SubjectID:   payload.SubjectID,
		MerchantID:  request.MerchantID,
		RoleID:      payload.RoleID,
		ExpiresAt:   payload.ExpiresAt,
		Actor:       actor,
		Reason:      request.Reason,
	})
	if err != nil {
		return "", err
	}
	return binding.ID, nil
}

// applyBindingDelete 解绑。
func (a *RoleChangeApplicator) applyBindingDelete(ctx context.Context, request *domain.ChangeRequest, actor string) error {
	return a.bindings.Unbind(ctx, UnbindCommand{
		ID:         request.TargetID,
		MerchantID: request.MerchantID,
		Actor:      actor,
		Reason:     request.Reason,
	})
}

// applyBreakglassRequest 创建一条限时绑定到系统破玻璃角色，并返回绑定 ID。
//
// 角色由编码定位而非载荷传入：调用方无法把请求指向任意高危角色。
func (a *RoleChangeApplicator) applyBreakglassRequest(
	ctx context.Context,
	request *domain.ChangeRequest,
	actor string,
) (string, error) {
	if request.Channel != domain.ChannelAdmin {
		// 与 ChangeRequestService.Create 的闸门互为兜底：即使有历史单据以 biz 端
		// 落库，执行阶段也不允许签发超级权限。
		return "", apperr.Forbidden("breakglass is not available on channel biz")
	}
	var payload breakglassRequestPayload
	if err := decodeChangePayload(request, &payload); err != nil {
		return "", err
	}
	grant, err := domain.NewBreakglassGrant(domain.NewBreakglassGrantParams{
		Channel:     request.Channel,
		SubjectType: domain.SubjectType(payload.SubjectType),
		SubjectID:   payload.SubjectID,
		MerchantID:  payload.MerchantID,
		Duration:    time.Duration(payload.DurationSeconds) * time.Second,
	})
	if err != nil {
		return "", err
	}
	role, err := a.roles.LoadByCode(ctx, request.Channel, grant.MerchantID, domain.BreakglassRoleCode)
	if err != nil {
		return "", err
	}
	if role.Type != domain.RoleTypeBreakglass {
		return "", apperr.Internal("breakglass role has unexpected type "+string(role.Type), nil)
	}
	binding, err := a.bindings.Bind(ctx, BindCommand{
		Channel:         request.Channel,
		SubjectType:     grant.SubjectType,
		SubjectID:       grant.SubjectID,
		MerchantID:      grant.MerchantID,
		RoleID:          role.ID,
		ExpiresAt:       grant.BindingExpiry(),
		AllowBreakglass: true,
		Actor:           actor,
		Reason:          request.Reason,
	})
	if err != nil {
		return "", err
	}
	return binding.ID, nil
}

// RevokeBreakglass 解绑破玻璃绑定；不存在时按幂等成功处理。
func (a *RoleChangeApplicator) RevokeBreakglass(ctx context.Context, bindingID, actor, reason string) error {
	return a.bindings.Unbind(ctx, UnbindCommand{
		ID:     bindingID,
		Actor:  actor,
		Reason: reason,
	})
}

// decodeChangePayload 严格解码载荷：拒绝未知字段与多余 JSON 值。
//
// 宽松解码会让字段名拼错（如 permission / permissions）静默变成「空权限」，
// 审批通过后变成一次真实的收权操作，且无法从载荷本身看出意图差异。
func decodeChangePayload(request *domain.ChangeRequest, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(request.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return apperr.BadRequest("change payload is invalid: " + err.Error())
	}
	if err := decoder.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		return apperr.BadRequest("change payload must contain exactly one JSON object")
	}
	return nil
}

// permissionCodesFromStrings 把字符串权限码转为强类型切片。
func permissionCodesFromStrings(codes []string) ([]iam.PermissionCode, error) {
	out := make([]iam.PermissionCode, 0, len(codes))
	for _, code := range codes {
		trimmed := strings.TrimSpace(code)
		if trimmed == "" {
			return nil, apperr.BadRequest("permission code must not be empty")
		}
		out = append(out, iam.PermissionCode(trimmed))
	}
	return out, nil
}

// dataScopesFromPayload 把载荷数据范围转换为领域值对象。
func dataScopesFromPayload(payload []dataScopePayload) ([]domain.DataScope, error) {
	out := make([]domain.DataScope, 0, len(payload))
	for _, item := range payload {
		scope, err := domain.NewDataScope(item.Resource, domain.DataScopeType(item.Type), item.MerchantIDs)
		if err != nil {
			return nil, err
		}
		out = append(out, scope)
	}
	return out, nil
}
