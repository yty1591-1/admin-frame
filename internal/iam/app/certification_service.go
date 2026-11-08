package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// 访问复核审计属性键；保持稳定便于 SIEM 按活动 / 任务聚合。
const (
	attrCampaignID   = "campaign_id"
	attrTaskID       = "task_id"
	attrDecision     = "decision"
	attrTaskCount    = "task_count"
	attrExportFormat = "format"
)

// 自动动作使用固定 actor，避免把定时任务误记成某个管理员的操作。
const certificationSystemActor = "system:certification"

// certificationAutoReason 是系统自动过期 / 回收留痕原因。
const certificationAutoReason = "access certification due date passed"

// CertificationRevoker 是访问复核回收绑定的端口。
//
// 由 runtime 适配 BindingService.Unbind，确保回收复用既有的软撤销、
// 约束校验与审计链路，而不是另写一条解绑路径。
type CertificationRevoker interface {
	Revoke(ctx context.Context, bindingID, actor, reason string) error
}

// CertificationScope 是复核请求的数据作用域，由 transport 从 Principal 派生。
type CertificationScope struct {
	Channel    domain.Channel
	MerchantID string
}

// Validate 校验作用域自洽：A 端不得带商户，B 端必须带商户。
func (s CertificationScope) Validate() error {
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

// CertificationLimits 是访问复核的规模上限与安全开关。
type CertificationLimits struct {
	// RequireReason 创建活动 / 裁决是否强制填写原因；裁决原因始终由领域层强制。
	RequireReason bool
	// AllowBizChannel 是否允许 B 端创建复核活动。
	AllowBizChannel bool
	// AutoRevoke 到期后是否自动回收绑定；false 时只标记 expired。
	AutoRevoke bool
	// MaxTasksPerCampaign 单活动任务上限；≤0 时使用默认 10000。
	MaxTasksPerCampaign int
}

// CertificationServiceConfig 是访问复核用例的装配参数。
type CertificationServiceConfig struct {
	Campaigns  domain.CertificationCampaignRepository
	Tasks      domain.CertificationTaskRepository
	Bindings   domain.BindingRepository
	Roles      domain.RoleRepository
	Revoker    CertificationRevoker
	Recorder   ChangeRecorder
	UnitOfWork UnitOfWork
	IDs        id.Generator
	Audit      AuditMeta
	Limits     CertificationLimits
	Logger     *slog.Logger
}

// CertificationService 编排访问复核活动与任务。
//
// 权限点校验不在本层完成：由 transport 的鉴权中间件负责；
// 本层只负责租户可见性（跨商户一律 NotFound）与业务状态机。
type CertificationService struct {
	campaigns domain.CertificationCampaignRepository
	tasks     domain.CertificationTaskRepository
	bindings  domain.BindingRepository
	roles     domain.RoleRepository
	revoker   CertificationRevoker
	recorder  ChangeRecorder
	uow       UnitOfWork
	log       *slog.Logger
	ids       id.Generator
	audit     auditBuilder
	limits    CertificationLimits
}

// NewCertificationService 构造用例；缺失必要依赖时返回错误（fail-closed）。
func NewCertificationService(cfg CertificationServiceConfig) (*CertificationService, error) {
	if cfg.Campaigns == nil || cfg.Tasks == nil || cfg.Bindings == nil || cfg.Roles == nil {
		return nil, apperr.Internal("iam certification repositories are required", nil)
	}
	if cfg.Revoker == nil {
		return nil, apperr.Internal("iam certification revoker is required", nil)
	}
	if cfg.Recorder == nil {
		return nil, apperr.Internal("iam change recorder is required", nil)
	}
	if cfg.IDs == nil {
		return nil, apperr.Internal("iam id generator is required", nil)
	}
	if cfg.Limits.MaxTasksPerCampaign <= 0 {
		cfg.Limits.MaxTasksPerCampaign = 10000
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	uow := cfg.UnitOfWork
	if uow == nil {
		uow = NoopUnitOfWork{}
	}
	return &CertificationService{
		campaigns: cfg.Campaigns,
		tasks:     cfg.Tasks,
		bindings:  cfg.Bindings,
		roles:     cfg.Roles,
		revoker:   cfg.Revoker,
		recorder:  cfg.Recorder,
		uow:       uow,
		log:       logger,
		ids:       cfg.IDs,
		audit:     newAuditBuilder(cfg.IDs, cfg.Audit),
		limits:    cfg.Limits,
	}, nil
}

// CreateCertificationCampaignCommand 是创建复核活动入参。
type CreateCertificationCampaignCommand struct {
	Channel        domain.Channel
	MerchantID     string
	Name           string
	RoleIDs        []string
	PrivilegedOnly bool
	DueAt          time.Time
	Actor          string
	Reason         string
}

// CreateCampaign 创建草稿活动。
func (s *CertificationService) CreateCampaign(
	ctx context.Context, cmd CreateCertificationCampaignCommand,
) (*domain.CertificationCampaign, error) {
	scope := CertificationScope{Channel: cmd.Channel, MerchantID: strings.TrimSpace(cmd.MerchantID)}
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if scope.Channel == domain.ChannelBiz && !s.limits.AllowBizChannel {
		return nil, apperr.Forbidden("certification is not enabled on channel biz")
	}
	if s.limits.RequireReason && strings.TrimSpace(cmd.Reason) == "" {
		return nil, apperr.BadRequest("reason is required")
	}
	campaignID, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate certification campaign id", err)
	}
	campaign, err := domain.NewCertificationCampaign(domain.NewCertificationCampaignParams{
		ID:             campaignID,
		Name:           cmd.Name,
		Channel:        scope.Channel,
		MerchantID:     scope.MerchantID,
		RoleIDs:        cmd.RoleIDs,
		PrivilegedOnly: cmd.PrivilegedOnly,
		DueAt:          cmd.DueAt,
		CreatedBy:      cmd.Actor,
	})
	if err != nil {
		return nil, err
	}
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.campaigns.Save(ctx, campaign, 0); err != nil {
			return err
		}
		return s.audit.record(ctx, s.recorder, auditParams{
			action:       audit.ActionRBACCertification,
			actor:        campaign.CreatedBy,
			channel:      string(campaign.Channel),
			merchantID:   campaign.MerchantID,
			resourceType: ResourceTypeCertification,
			resourceID:   campaign.ID,
			outcome:      audit.OutcomeSuccess,
			reason:       strings.TrimSpace(cmd.Reason),
			attrs: map[string]string{
				attrOperation: "create",
				attrRoleIDs:   codesAttr(campaign.RoleIDs),
				"privileged":  boolAttr(campaign.PrivilegedOnly),
				"due_at":      campaign.DueAt.Format(time.RFC3339),
			},
		})
	}); err != nil {
		return nil, err
	}
	return campaign, nil
}

// ListCampaigns 按作用域列举活动；B 端强制商户过滤。
func (s *CertificationService) ListCampaigns(
	ctx context.Context, scope CertificationScope, filter domain.CertificationCampaignFilter,
) ([]*domain.CertificationCampaign, error) {
	filter, err := s.scopedCampaignFilter(scope, filter)
	if err != nil {
		return nil, err
	}
	return s.campaigns.List(ctx, filter)
}

// CountCampaigns 返回与 ListCampaigns 相同条件的总数。
func (s *CertificationService) CountCampaigns(
	ctx context.Context, scope CertificationScope, filter domain.CertificationCampaignFilter,
) (int, error) {
	filter, err := s.scopedCampaignFilter(scope, filter)
	if err != nil {
		return 0, err
	}
	return s.campaigns.Count(ctx, filter)
}

// GetCampaign 加载活动；跨端 / 跨商户返回 NotFound，不泄露资源存在性。
func (s *CertificationService) GetCampaign(
	ctx context.Context, campaignID string, scope CertificationScope,
) (*domain.CertificationCampaign, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	campaign, err := s.campaigns.FindByID(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	if !campaignVisibleTo(campaign, scope) {
		return nil, apperr.NotFound("certification campaign not found")
	}
	return campaign, nil
}

// ActivateCampaign 激活活动并生成任务清单。
//
// 生成是幂等的：重复调用不会产生重复任务，也不会覆盖已裁决任务。
func (s *CertificationService) ActivateCampaign(
	ctx context.Context, campaignID string, scope CertificationScope, actor string,
) (*domain.CertificationCampaign, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(actor) == "" {
		return nil, apperr.BadRequest("actor is required")
	}
	now := time.Now().UTC()
	var campaign *domain.CertificationCampaign
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		loaded, err := s.campaigns.FindByID(ctx, campaignID)
		if err != nil {
			return err
		}
		if !campaignVisibleTo(loaded, scope) {
			return apperr.NotFound("certification campaign not found")
		}
		expectedVersion := loaded.Version
		if err := loaded.Activate(now); err != nil {
			return err
		}
		generated, err := s.generateTasks(ctx, loaded, now)
		if err != nil {
			return err
		}
		// 先按本次生成量做上限拦截，避免内存实现（无事务回滚）留下半成品任务。
		if len(generated) > s.limits.MaxTasksPerCampaign {
			return apperr.BadRequest("certification task count exceeds limit")
		}
		if err := s.tasks.SaveAll(ctx, generated); err != nil {
			return err
		}
		total, err := s.tasks.CountByCampaign(ctx, loaded.ID)
		if err != nil {
			return err
		}
		if total > s.limits.MaxTasksPerCampaign {
			return apperr.BadRequest("certification task count exceeds limit")
		}
		if loaded.TaskCount != total {
			loaded.TaskCount = total
			loaded.Version++
		}
		if err := s.campaigns.Save(ctx, loaded, expectedVersion); err != nil {
			return err
		}
		if err := s.audit.record(ctx, s.recorder, auditParams{
			action:       audit.ActionRBACCertification,
			actor:        strings.TrimSpace(actor),
			channel:      string(loaded.Channel),
			merchantID:   loaded.MerchantID,
			resourceType: ResourceTypeCertification,
			resourceID:   loaded.ID,
			outcome:      audit.OutcomeSuccess,
			attrs: map[string]string{
				attrOperation: "activate",
				attrTaskCount: intAttr(total),
				"generated":   intAttr(len(generated)),
			},
		}); err != nil {
			return err
		}
		campaign = loaded
		return nil
	}); err != nil {
		return nil, err
	}
	return campaign, nil
}

// ListTasks 分页列举活动任务；先校验活动可见性。
func (s *CertificationService) ListTasks(
	ctx context.Context, campaignID string, scope CertificationScope, filter domain.CertificationTaskFilter,
) ([]*domain.CertificationTask, int, error) {
	if _, err := s.GetCampaign(ctx, campaignID, scope); err != nil {
		return nil, 0, err
	}
	if filter.Status != "" {
		if err := filter.Status.Validate(); err != nil {
			return nil, 0, err
		}
	}
	filter.CampaignID = campaignID
	tasks, err := s.tasks.ListPage(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.tasks.CountByCampaign(ctx, campaignID)
	if err != nil {
		return nil, 0, err
	}
	return tasks, total, nil
}

// DecideTask 记录人工裁决。
//
// 驳回时先比对绑定指纹：不一致或已撤销时标记 skipped 并返回冲突，
// 绝不盲目回收一条可能已被并发重新授权的绑定。
func (s *CertificationService) DecideTask(
	ctx context.Context, campaignID, taskID string, decision domain.CertificationDecision,
	reason, actor string, scope CertificationScope,
) (*domain.CertificationTask, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(actor) == "" {
		return nil, apperr.BadRequest("actor is required")
	}
	now := time.Now().UTC()
	var result *domain.CertificationTask
	var conflictErr error
	err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		campaign, err := s.campaigns.FindByID(ctx, campaignID)
		if err != nil {
			return err
		}
		if !campaignVisibleTo(campaign, scope) {
			return apperr.NotFound("certification campaign not found")
		}
		if !campaign.AcceptsDecisions(now) {
			return apperr.Conflict("certification campaign is not active or has passed its due date")
		}
		task, err := s.tasks.FindByID(ctx, taskID)
		if err != nil {
			return err
		}
		if task.CampaignID != campaign.ID {
			return apperr.NotFound("certification task not found")
		}
		// 重复提交相同裁决幂等成功；相反裁决由领域层拒绝。
		if task.Status == domain.CertificationTaskStatusConfirmed ||
			task.Status == domain.CertificationTaskStatusRevoked {
			if task.Decision == decision {
				result = task
				return nil
			}
		}
		expectedVersion := task.Version
		if err := decision.Validate(); err != nil {
			return err
		}
		if decision == domain.CertificationDecisionRevoke {
			binding, findErr := s.bindings.FindByID(ctx, task.BindingID)
			if findErr != nil || bindingFingerprintMismatch(task, binding, campaign) {
				if findErr != nil && !errors.Is(findErr, apperr.ErrNotFound) {
					return findErr
				}
				skipReason := "binding changed since certification task was generated"
				if err := task.MarkSkipped(actor, skipReason, now); err != nil {
					return err
				}
				if err := s.tasks.Save(ctx, task, expectedVersion); err != nil {
					return err
				}
				if err := s.recordDecision(ctx, campaign, task, "skip", actor, skipReason); err != nil {
					return err
				}
				result = task
				conflictErr = apperr.Conflict(skipReason)
				return nil
			}
			if err := task.Decide(decision, actor, reason, now); err != nil {
				return err
			}
			if err := s.tasks.Save(ctx, task, expectedVersion); err != nil {
				return err
			}
			if err := s.revoker.Revoke(ctx, binding.ID, actor, strings.TrimSpace(reason)); err != nil {
				return err
			}
			result = task
			return s.recordDecision(ctx, campaign, task, "revoke", actor, reason)
		}
		if err := task.Decide(decision, actor, reason, now); err != nil {
			return err
		}
		if err := s.tasks.Save(ctx, task, expectedVersion); err != nil {
			return err
		}
		result = task
		return s.recordDecision(ctx, campaign, task, "confirm", actor, reason)
	})
	if err != nil {
		return nil, err
	}
	if conflictErr != nil {
		return result, conflictErr
	}
	return result, nil
}

// CloseCampaign 关闭活动；重复关闭幂等成功。
func (s *CertificationService) CloseCampaign(
	ctx context.Context, campaignID string, scope CertificationScope, actor, reason string,
) (*domain.CertificationCampaign, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var campaign *domain.CertificationCampaign
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		loaded, err := s.campaigns.FindByID(ctx, campaignID)
		if err != nil {
			return err
		}
		if !campaignVisibleTo(loaded, scope) {
			return apperr.NotFound("certification campaign not found")
		}
		expectedVersion := loaded.Version
		if err := loaded.Close(now, actor); err != nil {
			return err
		}
		if err := s.campaigns.Save(ctx, loaded, expectedVersion); err != nil {
			return err
		}
		if err := s.audit.record(ctx, s.recorder, auditParams{
			action:       audit.ActionRBACCertification,
			actor:        strings.TrimSpace(actor),
			channel:      string(loaded.Channel),
			merchantID:   loaded.MerchantID,
			resourceType: ResourceTypeCertification,
			resourceID:   loaded.ID,
			outcome:      audit.OutcomeSuccess,
			reason:       strings.TrimSpace(reason),
			attrs:        map[string]string{attrOperation: "close"},
		}); err != nil {
			return err
		}
		campaign = loaded
		return nil
	}); err != nil {
		return nil, err
	}
	return campaign, nil
}

// AutoExpireOverdue 处理截止时间已过的待裁决任务。
//
// AutoRevoke=false 时只标记 expired；true 时先做与人工驳回相同的指纹校验，
// 校验失败标记 skipped 而不是回收。全部动作使用固定系统 actor。
func (s *CertificationService) AutoExpireOverdue(
	ctx context.Context, campaignID string, scope CertificationScope,
) (int, error) {
	if err := scope.Validate(); err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	affected := 0
	err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		campaign, err := s.campaigns.FindByID(ctx, campaignID)
		if err != nil {
			return err
		}
		if !campaignVisibleTo(campaign, scope) {
			return apperr.NotFound("certification campaign not found")
		}
		affected, err = s.expireOverdueTasks(ctx, campaign, now)
		return err
	})
	if err != nil {
		return 0, err
	}
	return affected, nil
}

// expireOverdueTasks 在调用方事务内处理单个活动的超期任务。
//
// 调用方必须先完成可见性 / 作用域校验：本函数只做状态机与回收，
// 不判断请求来源，避免扫描任务与人工触发出现两套判定。
func (s *CertificationService) expireOverdueTasks(
	ctx context.Context, campaign *domain.CertificationCampaign, now time.Time,
) (int, error) {
	if campaign.Status != domain.CertificationStatusActive {
		return 0, apperr.Conflict("certification campaign is not active")
	}
	if !campaign.IsOverdue(now) {
		return 0, nil
	}
	affected := 0
	all, err := s.tasks.ListByCampaign(ctx, campaign.ID)
	if err != nil {
		return 0, err
	}
	for _, task := range all {
		if task.Status != domain.CertificationTaskStatusPending {
			continue
		}
		expectedVersion := task.Version
		if !s.limits.AutoRevoke {
			if err := task.Expire(now); err != nil {
				return 0, err
			}
			if err := s.tasks.Save(ctx, task, expectedVersion); err != nil {
				return 0, err
			}
			if err := s.recordDecision(ctx, campaign, task, "auto_expire", certificationSystemActor, certificationAutoReason); err != nil {
				return 0, err
			}
			affected++
			continue
		}
		binding, findErr := s.bindings.FindByID(ctx, task.BindingID)
		if findErr != nil || bindingFingerprintMismatch(task, binding, campaign) {
			if findErr != nil && !errors.Is(findErr, apperr.ErrNotFound) {
				return 0, findErr
			}
			if err := task.MarkSkipped(certificationSystemActor, certificationAutoReason, now); err != nil {
				return 0, err
			}
			if err := s.tasks.Save(ctx, task, expectedVersion); err != nil {
				return 0, err
			}
			if err := s.recordDecision(ctx, campaign, task, "auto_skip", certificationSystemActor, certificationAutoReason); err != nil {
				return 0, err
			}
			affected++
			continue
		}
		if err := task.Decide(domain.CertificationDecisionRevoke, certificationSystemActor, certificationAutoReason, now); err != nil {
			return 0, err
		}
		if err := s.tasks.Save(ctx, task, expectedVersion); err != nil {
			return 0, err
		}
		if err := s.revoker.Revoke(ctx, binding.ID, certificationSystemActor, certificationAutoReason); err != nil {
			return 0, err
		}
		if err := s.recordDecision(ctx, campaign, task, "auto_revoke", certificationSystemActor, certificationAutoReason); err != nil {
			return 0, err
		}
		affected++
	}
	return affected, nil
}

// EvidenceRecord 是证据导出的稳定行结构；CSV 与 JSON 使用同一字段来源。
type EvidenceRecord struct {
	CampaignID   string     `json:"campaign_id"`
	CampaignName string     `json:"campaign_name"`
	TaskID       string     `json:"task_id"`
	SubjectID    string     `json:"subject_id"`
	RoleID       string     `json:"role_id"`
	BindingID    string     `json:"binding_id"`
	ReviewerID   string     `json:"reviewer_id"`
	Status       string     `json:"status"`
	Decision     string     `json:"decision"`
	Reason       string     `json:"reason"`
	DecidedBy    string     `json:"decided_by"`
	DecidedAt    *time.Time `json:"decided_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

// evidenceColumns 是 CSV 表头；顺序作为导出契约稳定不变。
var evidenceColumns = []string{
	"campaign_id", "campaign_name", "task_id", "subject_id", "role_id", "binding_id",
	"reviewer_id", "status", "decision", "reason", "decided_by", "decided_at", "created_at",
}

// ExportEvidence 导出活动全量证据（CSV / JSON）。
func (s *CertificationService) ExportEvidence(
	ctx context.Context, campaignID string, scope CertificationScope, format, actor string,
) (payload []byte, contentType string, err error) {
	campaign, err := s.GetCampaign(ctx, campaignID, scope)
	if err != nil {
		return nil, "", err
	}
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "json" {
		return nil, "", apperr.BadRequest("format must be csv or json")
	}
	tasks, err := s.tasks.ListByCampaign(ctx, campaign.ID)
	if err != nil {
		return nil, "", err
	}
	records := make([]EvidenceRecord, 0, len(tasks))
	for _, task := range tasks {
		records = append(records, EvidenceRecord{
			CampaignID:   campaign.ID,
			CampaignName: campaign.Name,
			TaskID:       task.ID,
			SubjectID:    task.SubjectID,
			RoleID:       task.RoleID,
			BindingID:    task.BindingID,
			ReviewerID:   task.ReviewerID,
			Status:       string(task.Status),
			Decision:     string(task.Decision),
			Reason:       task.Reason,
			DecidedBy:    task.DecidedBy,
			DecidedAt:    task.DecidedAt,
			CreatedAt:    task.CreatedAt,
		})
	}

	if format == "json" {
		payload, err = json.Marshal(records)
		if err != nil {
			return nil, "", apperr.Internal("encode certification evidence", err)
		}
		contentType = "application/json; charset=utf-8"
	} else {
		payload, contentType, err = encodeEvidenceCSV(records)
		if err != nil {
			return nil, "", err
		}
	}
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		return s.audit.record(ctx, s.recorder, auditParams{
			action:       audit.ActionRBACCertification,
			actor:        strings.TrimSpace(actor),
			channel:      string(campaign.Channel),
			merchantID:   campaign.MerchantID,
			resourceType: ResourceTypeCertification,
			resourceID:   campaign.ID,
			outcome:      audit.OutcomeSuccess,
			attrs: map[string]string{
				attrOperation:    "export",
				attrExportFormat: format,
				attrTaskCount:    intAttr(len(records)),
			},
		})
	}); err != nil {
		return nil, "", err
	}
	return payload, contentType, nil
}

// generateTasks 依据活动范围生成待复核任务。
func (s *CertificationService) generateTasks(
	ctx context.Context, campaign *domain.CertificationCampaign, now time.Time,
) ([]*domain.CertificationTask, error) {
	roleScope := make(map[string]struct{}, len(campaign.RoleIDs))
	for _, roleID := range campaign.RoleIDs {
		roleScope[roleID] = struct{}{}
	}
	bindings, err := s.bindings.ListEffectiveByTenant(ctx, campaign.Channel, campaign.MerchantID, now)
	if err != nil {
		return nil, err
	}
	// 特权判定按角色缓存：SQL 仓储的 FindByID 需要 3 条查询（角色 + 权限 + 数据范围），
	// 若按绑定逐条判定，1 万绑定就是 3 万次往返（实测 49s → 15s 级）。
	// 缓存只在本次活动生成内有效，语义与逐条判定等价（角色在生成期间不可变）。
	privilegedCache := make(map[string]bool, len(roleScope))
	tasks := make([]*domain.CertificationTask, 0, len(bindings))
	for _, binding := range bindings {
		if binding == nil || binding.IsRevoked() || binding.IsExpired(now) {
			continue
		}
		if len(roleScope) > 0 {
			if _, ok := roleScope[binding.RoleID]; !ok {
				continue
			}
		}
		if campaign.PrivilegedOnly {
			privileged, cached := privilegedCache[binding.RoleID]
			if !cached {
				privileged, err = s.isPrivilegedRole(ctx, binding.RoleID)
				if err != nil {
					return nil, err
				}
				privilegedCache[binding.RoleID] = privileged
			}
			if !privileged {
				continue
			}
		}
		taskID, err := s.ids.NextString()
		if err != nil {
			return nil, apperr.Internal("generate certification task id", err)
		}
		task, err := domain.NewCertificationTask(domain.NewCertificationTaskParams{
			ID:                 taskID,
			CampaignID:         campaign.ID,
			SubjectType:        binding.SubjectType,
			SubjectID:          binding.SubjectID,
			MerchantID:         binding.MerchantID,
			RoleID:             binding.RoleID,
			BindingID:          binding.ID,
			BindingFingerprint: domain.BindingFingerprint(binding),
			ReviewerID:         campaign.CreatedBy,
		})
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

// isPrivilegedRole 判断角色是否含高危权限：iam:* / data:export / config:*。
func (s *CertificationService) isPrivilegedRole(ctx context.Context, roleID string) (bool, error) {
	role, err := s.roles.FindByID(ctx, roleID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// 角色不存在的绑定属于脏数据：高危及复核场景都按「非特权」跳过，
			// 由绑定清理任务另行处理，避免阻断整次复核。
			return false, nil
		}
		return false, err
	}
	for code := range role.Permissions {
		value := string(code)
		if strings.HasPrefix(value, "iam:") || value == "data:export" || strings.HasPrefix(value, "config:") {
			return true, nil
		}
	}
	return false, nil
}

// recordDecision 写裁决 / 自动动作审计；失败由调用方回滚。
func (s *CertificationService) recordDecision(
	ctx context.Context, campaign *domain.CertificationCampaign, task *domain.CertificationTask,
	operation, actor, reason string,
) error {
	return s.audit.record(ctx, s.recorder, auditParams{
		action:       audit.ActionRBACCertification,
		actor:        strings.TrimSpace(actor),
		channel:      string(campaign.Channel),
		merchantID:   campaign.MerchantID,
		resourceType: ResourceTypeCertification,
		resourceID:   task.ID,
		outcome:      audit.OutcomeSuccess,
		reason:       strings.TrimSpace(reason),
		attrs: map[string]string{
			attrOperation:  operation,
			attrCampaignID: campaign.ID,
			attrTaskID:     task.ID,
			attrDecision:   string(task.Decision),
			attrBindingID:  task.BindingID,
			attrSubjectID:  task.SubjectID,
			attrRoleID:     task.RoleID,
		},
	})
}

// bindingFingerprintMismatch 判断任务对应的绑定是否仍与生成时一致。
func bindingFingerprintMismatch(
	task *domain.CertificationTask, binding *domain.Binding, campaign *domain.CertificationCampaign,
) bool {
	if binding == nil {
		return true
	}
	if binding.IsRevoked() || binding.IsExpired(time.Now().UTC()) {
		return true
	}
	if binding.Channel != campaign.Channel || binding.MerchantID != campaign.MerchantID {
		return true
	}
	return domain.BindingFingerprint(binding) != task.BindingFingerprint
}

// campaignVisibleTo 判定活动是否属于请求作用域。
func campaignVisibleTo(campaign *domain.CertificationCampaign, scope CertificationScope) bool {
	return campaign.Channel == scope.Channel && campaign.MerchantID == scope.MerchantID
}

// scopedCampaignFilter 强制端别 / 商户过滤，忽略调用方自报的越权条件。
func (s *CertificationService) scopedCampaignFilter(
	scope CertificationScope, filter domain.CertificationCampaignFilter,
) (domain.CertificationCampaignFilter, error) {
	if err := scope.Validate(); err != nil {
		return filter, err
	}
	if filter.Status != "" {
		if err := filter.Status.Validate(); err != nil {
			return filter, err
		}
	}
	filter.Channel = scope.Channel
	filter.MerchantID = scope.MerchantID
	return filter, nil
}

// encodeEvidenceCSV 使用标准库 csv 输出稳定列序。
func encodeEvidenceCSV(records []EvidenceRecord) (payload []byte, contentType string, err error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write(evidenceColumns); err != nil {
		return nil, "", apperr.Internal("write certification evidence header", err)
	}
	for _, record := range records {
		decidedAt := ""
		if record.DecidedAt != nil {
			decidedAt = record.DecidedAt.UTC().Format(time.RFC3339)
		}
		row := []string{
			record.CampaignID, record.CampaignName, record.TaskID, record.SubjectID,
			record.RoleID, record.BindingID, record.ReviewerID, record.Status,
			record.Decision, record.Reason, record.DecidedBy, decidedAt,
			record.CreatedAt.UTC().Format(time.RFC3339),
		}
		if err := writer.Write(row); err != nil {
			return nil, "", apperr.Internal("write certification evidence row", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, "", apperr.Internal("flush certification evidence", err)
	}
	return buffer.Bytes(), "text/csv; charset=utf-8", nil
}

// boolAttr 把布尔值编码为审计友好的稳定字面量。
func boolAttr(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// intAttr 把计数编码为审计属性。
func intAttr(value int) string { return strconv.Itoa(value) }
