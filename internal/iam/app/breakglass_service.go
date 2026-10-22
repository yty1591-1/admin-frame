package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// BreakglassService 是破玻璃（限时超级权限）用例。
//
// 设计要点（docs/rbac-iam.md §8.5）：
//   - 申请只是一张 action=breakglass_request 的审批单，本身不产生任何授权；
//   - 审批人与申请人必须不同（复用 ChangeRequest 的 SoD），审批通过后立即应用：
//     创建一条 expires_at ≤ 2h 的绑定到系统 breakglass 角色；
//   - 超级权限不在权限目录中，只由快照在绑定期内展开 "*"；
//   - 申请 / 审批 / 撤销全部写 rbac.breakglass 审计（AUD-01）。
type BreakglassService struct {
	changes  *ChangeRequestService
	recorder ChangeRecorder
	audit    auditBuilder
	limits   BreakglassLimits
	log      *slog.Logger
}

// BreakglassLimits 是破玻璃开关与允许主体名单。
type BreakglassLimits struct {
	// Enabled 是否启用破玻璃；生产默认关闭，由 iam.breakglass.enabled 控制。
	Enabled bool
	// AllowedSubjects 允许申请破玻璃的主体白名单；非空时名单外主体一律 403。
	AllowedSubjects []string
}

// BreakglassServiceConfig 是 BreakglassService 的装配参数。
type BreakglassServiceConfig struct {
	Changes  *ChangeRequestService
	Recorder ChangeRecorder
	IDs      id.Generator
	Audit    AuditMeta
	Limits   BreakglassLimits
	Logger   *slog.Logger
}

// NewBreakglassService 构造破玻璃用例；缺失审批服务时返回错误（fail-closed）。
func NewBreakglassService(cfg BreakglassServiceConfig) (*BreakglassService, error) {
	if cfg.Changes == nil {
		return nil, apperr.Internal("iam change request service is required for breakglass", nil)
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &BreakglassService{
		changes:  cfg.Changes,
		recorder: cfg.Recorder,
		audit:    newAuditBuilder(cfg.IDs, cfg.Audit),
		limits:   cfg.Limits,
		log:      logger,
	}, nil
}

// RequestBreakglassCommand 是发起破玻璃申请的入参。
type RequestBreakglassCommand struct {
	// SubjectType 主体类型；A 端固定 staff。
	SubjectType string
	// SubjectID 被授权主体 ID。
	SubjectID string
	// Duration 授权时长，必须 ≤ 2h。
	Duration time.Duration
	// Reason 申请原因。
	Reason string
	// Actor 申请人 subject_id。
	Actor string
}

// Request 发起破玻璃申请（不产生授权）。
func (s *BreakglassService) Request(ctx context.Context, cmd RequestBreakglassCommand) (*domain.ChangeRequest, error) {
	if !s.limits.Enabled {
		return nil, apperr.Forbidden("breakglass is disabled")
	}
	if strings.TrimSpace(cmd.Actor) == "" {
		return nil, apperr.BadRequest("actor is required")
	}
	if len(s.limits.AllowedSubjects) > 0 && !slices.Contains(s.limits.AllowedSubjects, cmd.SubjectID) {
		return nil, apperr.Forbidden("subject is not allowed to use breakglass")
	}
	payload, err := json.Marshal(breakglassRequestPayload{
		SubjectType:     cmd.SubjectType,
		SubjectID:       cmd.SubjectID,
		DurationSeconds: int(cmd.Duration / time.Second),
	})
	if err != nil {
		return nil, apperr.Internal("encode breakglass payload", err)
	}
	request, err := s.changes.Create(ctx, CreateChangeRequestCommand{
		Channel: domain.ChannelAdmin,
		Action:  domain.ChangeActionBreakglassRequest,
		Payload: payload,
		Reason:  cmd.Reason,
		Actor:   cmd.Actor,
	})
	if err != nil {
		return nil, err
	}
	if err := s.record(ctx, request, "request", cmd.Actor, nil); err != nil {
		// 审计失败视为申请无效：尽力撤销，避免留下一张没有申请事实的单子。
		if _, cancelErr := s.changes.Cancel(ctx, request.ID, request.Version, cmd.Actor); cancelErr != nil {
			s.log.WarnContext(ctx, "iam breakglass request audit failed and cancel failed",
				"change_request_id", request.ID, "err", cancelErr)
		}
		return nil, err
	}
	return request, nil
}

// ApproveBreakglassCommand 是审批并生效破玻璃的入参。
type ApproveBreakglassCommand struct {
	ID       string
	Version  int64
	Approver string
}

// Approve 审批通过并立即生效；已生效的单子按幂等成功返回。
//
// 失败语义：审批已通过但应用失败时，审批单保持 approved，调用方可用同一端点
// 重试（此时 version 取服务端当前值）。
func (s *BreakglassService) Approve(ctx context.Context, cmd ApproveBreakglassCommand) (*domain.ChangeRequest, error) {
	request, err := s.changes.Get(ctx, cmd.ID, domain.ChannelAdmin)
	if err != nil {
		return nil, err
	}
	if request.Action != domain.ChangeActionBreakglassRequest {
		return nil, apperr.BadRequest("change request is not a breakglass request")
	}

	current := request
	switch {
	case request.IsPending():
		current, err = s.changes.Approve(ctx, request.ID, cmd.Version, cmd.Approver)
	case request.IsApproved():
		// 上次应用失败后的重试；version 以服务端为准。
		current, err = s.changes.Get(ctx, request.ID, domain.ChannelAdmin)
	case request.Status == domain.ChangeStatusApplied:
		return request, nil
	default:
		return nil, apperr.Conflict("breakglass request is not pending or approved")
	}
	if err != nil {
		return nil, err
	}

	applied, err := s.changes.Apply(ctx, ApplyChangeRequestCommand{
		ID:      current.ID,
		Version: current.Version,
		Actor:   cmd.Approver,
	})
	if err != nil {
		return nil, err
	}
	if err := s.record(ctx, applied, "approve", cmd.Approver, map[string]string{
		attrBindingID: applied.TargetID,
	}); err != nil {
		return nil, err
	}
	return applied, nil
}

// Revoke 提前撤销已生效的破玻璃绑定。
//
// version 必填：撤销是不可逆操作，必须证明调用方看过当前审批单版本。
func (s *BreakglassService) Revoke(
	ctx context.Context,
	requestID string,
	version int64,
	actor string,
) (*domain.ChangeRequest, error) {
	request, err := s.changes.Get(ctx, requestID, domain.ChannelAdmin)
	if err != nil {
		return nil, err
	}
	if request.Action != domain.ChangeActionBreakglassRequest {
		return nil, apperr.BadRequest("change request is not a breakglass request")
	}
	if request.Version != version {
		return nil, apperr.Conflict("change request version conflict")
	}
	if err := s.changes.RevokeBreakglass(ctx, requestID, actor); err != nil {
		return nil, err
	}
	if err := s.record(ctx, request, "revoke", actor, map[string]string{
		attrBindingID: request.TargetID,
	}); err != nil {
		return nil, err
	}
	return request, nil
}

// record 写 rbac.breakglass 审计；属性只放稳定低基数字段。
func (s *BreakglassService) record(
	ctx context.Context,
	request *domain.ChangeRequest,
	operation string,
	actor string,
	extra map[string]string,
) error {
	attrs := map[string]string{attrOperation: operation}
	if decoded, err := decodeBreakglassRequest(request); err == nil {
		attrs[attrSubjectType] = string(decoded.SubjectType)
		attrs[attrSubjectID] = decoded.SubjectID
	}
	for key, value := range extra {
		attrs[key] = value
	}
	return s.audit.record(ctx, s.recorder, auditParams{
		action:       audit.ActionRBACBreakglass,
		actor:        actor,
		channel:      string(request.Channel),
		resourceType: ResourceTypeBreakglass,
		resourceID:   request.ID,
		outcome:      audit.OutcomeSuccess,
		reason:       request.Reason,
		attrs:        attrs,
	})
}

// decodeBreakglassRequest 解码已通过创建阶段校验的载荷。
func decodeBreakglassRequest(request *domain.ChangeRequest) (breakglassRequestPayload, error) {
	var payload breakglassRequestPayload
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		return breakglassRequestPayload{}, err
	}
	return payload, nil
}
