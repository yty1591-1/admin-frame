// Package app 是 featureflag 用例层：管理（FlagService）、评估
// （EvaluationService）、到期治理（ExpireService）。
//
// 审计动作（docs/audit/EVENTS.md 的 featureflag.* 段）：
// 变更全走审计且与仓储写入同事务（Outbox 出口 fail closed）；
// 到期禁用为 worker 元审计（actor_type=system）。
package app

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/featureflag/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// 审计动作（EVENTS.md featureflag.* 段）。
const (
	ActionFlagCreate  = "featureflag.create"
	ActionFlagUpdate  = "featureflag.update"
	ActionFlagEnable  = "featureflag.enable"
	ActionFlagDisable = "featureflag.disable"
	ActionFlagDelete  = "featureflag.delete"
	ActionFlagExpire  = "featureflag.expire"
)

// ResourceTypeFlag 是审计资源类型。
const ResourceTypeFlag = "feature_flag"

// Actor 是审计主体（对齐 tenant app.Actor 形态）。
type Actor struct {
	SubjectID  string
	ActorType  string
	Channel    string
	MerchantID string
	TraceID    string
}

// Options 是用例参数。
type Options struct {
	// Service 服务名，写入审计事件。
	Service string
	// Observer 审计写出失败计数（nil 安全由构造保证）。
	Observer EvaluationObserver
	// Now 时钟；测试注入（FlagService 经 FlagServiceOptions.Now 注入，
	// 其余服务共用本字段）。
	Now func() time.Time
}

type auditContext struct {
	emitter  audit.Emitter
	ids      id.Generator
	service  string
	now      func() time.Time
	observer EvaluationObserver
}

// emit 写一条审计事件；返回错误由调用方决定回滚（Outbox 模式 fail closed）。
func (a auditContext) emit(ctx context.Context, actor Actor, action, resourceID string,
	outcome audit.Outcome, reason string, attrs map[string]string) error {
	eventID := ""
	if a.ids != nil {
		if generated, err := a.ids.NextString(); err == nil {
			eventID = generated
		}
	}
	if eventID == "" {
		eventID = "featureflag-" + a.now().UTC().Format("20060102150405.000000000")
	}
	event := audit.Event{
		EventID:      eventID,
		Action:       action,
		ActorID:      actor.SubjectID,
		ActorType:    actorType(actor),
		Channel:      channel(actor.Channel),
		MerchantID:   actor.MerchantID,
		ResourceType: ResourceTypeFlag,
		ResourceID:   resourceID,
		Outcome:      outcome,
		OccurredAt:   a.now().UTC(),
		TraceID:      actor.TraceID,
		Service:      a.service,
		Reason:       reason,
		Attrs:        attrs,
	}
	if err := a.emitter.Emit(ctx, event); err != nil {
		if a.observer != nil {
			a.observer.EvalObserved("audit", "emit_failed")
		}
		return err
	}
	return nil
}

func actorType(a Actor) string {
	if strings.TrimSpace(a.ActorType) == "" {
		return "user"
	}
	return a.ActorType
}

// channel 归一端别；空视为 admin（flag 管理仅 A 端）。
func channel(c string) string {
	if strings.TrimSpace(c) == "" {
		return domain.ChannelAdmin
	}
	return c
}

// FlagService 是 flag 管理用例（CRUD / 启停 / 删除）。
type FlagService struct {
	repos     domain.Repository
	evaluator *EvaluationService // 变更后失效本进程缓存
	audit     auditContext
	now       func() time.Time
}

// FlagServiceOptions 构造参数。
type FlagServiceOptions struct {
	// Evaluator 本进程评估服务（变更后失效缓存）；nil 安全（单测）。
	Evaluator *EvaluationService
	// Now 时钟；测试注入。
	Now func() time.Time
}

// NewFlagService 构造管理用例；仓储与审计出口必填。
func NewFlagService(repos domain.Repository, emitter audit.Emitter, ids id.Generator,
	opts Options, svcOpts FlagServiceOptions) (*FlagService, error) {
	switch {
	case repos == nil:
		return nil, apperr.Internal("flag service requires a repository", nil)
	case emitter == nil:
		return nil, apperr.Internal("flag service requires an audit emitter", nil)
	}
	now := svcOpts.Now
	if now == nil {
		now = time.Now
	}
	return &FlagService{
		repos:     repos,
		evaluator: svcOpts.Evaluator,
		audit: auditContext{
			emitter: emitter, ids: ids, service: opts.Service,
			now: now, observer: opts.Observer,
		},
		now: now,
	}, nil
}

// CreateInput 是创建入参。
type CreateInput struct {
	Key            string
	Description    string
	Rules          []domain.Rule
	DefaultEnabled bool
	Permanent      bool
	ExpiresAt      time.Time
	Actor          Actor
}

// Create 新建 flag。
//
// 防御性默认（§6 验收）：DefaultEnabled=true 且无任何规则 = 裸全量放开，
// 直接拒绝——新 flag 必须先配规则或从 default 关闭开始。
func (s *FlagService) Create(ctx context.Context, in CreateInput) (domain.Flag, error) {
	if in.DefaultEnabled && len(in.Rules) == 0 {
		return domain.Flag{}, apperr.BadRequest("default_enabled=true without rules is a bare full rollout; start disabled")
	}
	flag, err := domain.NewFlag(in.Key, in.Description, in.Rules, in.Permanent, in.ExpiresAt,
		actorKey(in.Actor), s.now())
	if err != nil {
		return domain.Flag{}, translate(err)
	}
	if err := s.repos.Create(ctx, flag); err != nil {
		switch err {
		case domain.ErrDuplicateKey:
			return domain.Flag{}, apperr.Conflict("flag key already exists")
		default:
			return domain.Flag{}, apperr.Internal("create flag failed", err)
		}
	}
	if err := s.audit.emit(ctx, in.Actor, ActionFlagCreate, flag.Key,
		audit.OutcomeSuccess, "", createAttrs(flag)); err != nil {
		return domain.Flag{}, apperr.Internal("emit flag audit failed", err)
	}
	s.invalidate(flag.Key)
	return flag, nil
}

// Get 读取 flag 详情。
func (s *FlagService) Get(ctx context.Context, key string) (domain.Flag, error) {
	flag, err := s.repos.Get(ctx, key)
	if err != nil {
		if err == domain.ErrNotFound {
			return domain.Flag{}, apperr.NotFound("feature flag not found")
		}
		return domain.Flag{}, apperr.Internal("get flag failed", err)
	}
	return flag, nil
}

// List 按 state 过滤列出。
func (s *FlagService) List(ctx context.Context, state string, limit, offset int) ([]domain.Flag, int, error) {
	if state != "" && !validStateFilter(state) {
		return nil, 0, apperr.BadRequest("state must be one of enabled, disabled, expired")
	}
	flags, total, err := s.repos.List(ctx, state, limit, offset)
	if err != nil {
		return nil, 0, apperr.Internal("list flags failed", err)
	}
	return flags, total, nil
}

// UpdateInput 是变更入参（乐观锁）。
type UpdateInput struct {
	Key             string
	Description     *string
	Rules           *[]domain.Rule
	DefaultEnabled  *bool
	ExpiresAt       *time.Time
	ExpectedVersion int64
	Actor           Actor
}

// Update 按字段可选变更；kills 语义：nil 字段保持原值。
func (s *FlagService) Update(ctx context.Context, in UpdateInput) (domain.Flag, error) {
	flag, err := s.repos.Get(ctx, in.Key)
	if err != nil {
		if err == domain.ErrNotFound {
			return domain.Flag{}, apperr.NotFound("feature flag not found")
		}
		return domain.Flag{}, apperr.Internal("get flag failed", err)
	}
	if flag.Status == domain.StatusExpired {
		return domain.Flag{}, apperr.Conflict("expired flag must be recreated with new expiry, not edited")
	}
	next := flag
	if in.Description != nil {
		next.Description = strings.TrimSpace(*in.Description)
	}
	if in.Rules != nil {
		if len(*in.Rules) > domain.MaxRules {
			return domain.Flag{}, apperr.BadRequest("too many rules")
		}
		for i := range *in.Rules {
			if err := (*in.Rules)[i].Validate(); err != nil {
				return domain.Flag{}, apperr.BadRequest(err.Error())
			}
		}
		next.Rules = append([]domain.Rule(nil), (*in.Rules)...)
	}
	if in.DefaultEnabled != nil {
		if *in.DefaultEnabled && len(next.Rules) == 0 {
			return domain.Flag{}, apperr.BadRequest("default_enabled=true without rules is a bare full rollout; add rules first")
		}
		next.DefaultEnabled = *in.DefaultEnabled
	}
	if in.ExpiresAt != nil {
		if next.Permanent {
			return domain.Flag{}, apperr.BadRequest("permanent flag carries no expiry")
		}
		if !in.ExpiresAt.IsZero() && !in.ExpiresAt.After(s.now()) {
			return domain.Flag{}, apperr.BadRequest("expires_at must be in the future")
		}
		next.ExpiresAt = *in.ExpiresAt
	}
	if err := next.Validate(); err != nil {
		return domain.Flag{}, apperr.BadRequest(err.Error())
	}
	next.UpdatedAt = s.now()
	if err := s.repos.Update(ctx, next, in.ExpectedVersion); err != nil {
		if err == domain.ErrVersionConflict {
			return domain.Flag{}, apperr.Conflict("flag was modified concurrently")
		}
		if err == domain.ErrNotFound {
			return domain.Flag{}, apperr.NotFound("feature flag not found")
		}
		return domain.Flag{}, apperr.Internal("update flag failed", err)
	}
	if err := s.audit.emit(ctx, in.Actor, ActionFlagUpdate, next.Key,
		audit.OutcomeSuccess, "", updateAttrs(flag, next)); err != nil {
		return domain.Flag{}, apperr.Internal("emit flag audit failed", err)
	}
	s.invalidate(next.Key)
	return next, nil
}

// SetStatus 启停（kill switch 的管理入口）。
func (s *FlagService) SetStatus(ctx context.Context, key string, target domain.FlagStatus,
	expectedVersion int64, actor Actor) (domain.Flag, error) {
	flag, err := s.repos.Get(ctx, key)
	if err != nil {
		if err == domain.ErrNotFound {
			return domain.Flag{}, apperr.NotFound("feature flag not found")
		}
		return domain.Flag{}, apperr.Internal("get flag failed", err)
	}
	next, err := flag.Transition(target, s.now())
	if err != nil {
		return domain.Flag{}, apperr.Conflict("invalid status transition: " + err.Error())
	}
	next.Version = flag.Version + 1
	if err := s.repos.Update(ctx, next, expectedVersion); err != nil {
		if err == domain.ErrVersionConflict {
			return domain.Flag{}, apperr.Conflict("flag was modified concurrently")
		}
		return domain.Flag{}, apperr.Internal("update flag status failed", err)
	}
	action := ActionFlagEnable
	if target == domain.StatusDisabled {
		action = ActionFlagDisable
	}
	if err := s.audit.emit(ctx, actor, action, next.Key, audit.OutcomeSuccess, "", nil); err != nil {
		return domain.Flag{}, apperr.Internal("emit flag audit failed", err)
	}
	s.invalidate(next.Key)
	return next, nil
}

// Delete 物理删除；仅允许对非 enabled 状态执行（启用中的 flag 必须先停用，
// 防止误删在用的 kill switch）。
func (s *FlagService) Delete(ctx context.Context, key string, actor Actor) error {
	flag, err := s.repos.Get(ctx, key)
	if err != nil {
		if err == domain.ErrNotFound {
			return apperr.NotFound("feature flag not found")
		}
		return apperr.Internal("get flag failed", err)
	}
	if flag.Status == domain.StatusEnabled {
		return apperr.Conflict("enabled flag must be disabled before delete")
	}
	if err := s.repos.Delete(ctx, key); err != nil {
		return apperr.Internal("delete flag failed", err)
	}
	if err := s.audit.emit(ctx, actor, ActionFlagDelete, key, audit.OutcomeSuccess, "", nil); err != nil {
		return apperr.Internal("emit flag audit failed", err)
	}
	s.invalidate(key)
	return nil
}

// invalidate 变更后失效本进程评估缓存（跨副本靠 TTL）。
func (s *FlagService) invalidate(key string) {
	if s.evaluator != nil {
		s.evaluator.Invalidate()
	}
	_ = key // 缓存为整体失效；key 参数保留语义可读性。
}

func actorKey(a Actor) string {
	if strings.TrimSpace(a.SubjectID) == "" {
		return "system"
	}
	return a.SubjectID
}

func validStateFilter(state string) bool {
	return state == string(domain.StatusEnabled) ||
		state == string(domain.StatusDisabled) ||
		state == string(domain.StatusExpired)
}

func createAttrs(f domain.Flag) map[string]string {
	return map[string]string{
		"permanent": boolString(f.Permanent),
		"default":   boolString(f.DefaultEnabled),
		"rules":     itoa(len(f.Rules)),
	}
}

func updateAttrs(before, after domain.Flag) map[string]string {
	var fields []string
	if before.Description != after.Description {
		fields = append(fields, "description")
	}
	if rulesSignature(before.Rules) != rulesSignature(after.Rules) {
		fields = append(fields, "rules")
	}
	if before.DefaultEnabled != after.DefaultEnabled {
		fields = append(fields, "default_enabled")
	}
	if !before.ExpiresAt.Equal(after.ExpiresAt) {
		fields = append(fields, "expires_at")
	}
	return map[string]string{
		"fields":  strings.Join(fields, ","),
		"version": itoa64(after.Version),
	}
}

// rulesSignature 规则数组指纹（仅用于审计 diff 展示，不承担安全语义）。
func rulesSignature(rules []domain.Rule) string {
	parts := make([]string, 0, len(rules))
	for _, r := range rules {
		parts = append(parts, r.Kind())
	}
	return strings.Join(parts, "|")
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func itoa(v int) string { return strconv.Itoa(v) }

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

// translate 领域错误 → 业务码；未知错误包 500。
func translate(err error) error {
	switch err {
	case nil:
		return nil
	case domain.ErrKeyRequired, domain.ErrKeyTooLong, domain.ErrKeyInvalid,
		domain.ErrDescriptionTooLong, domain.ErrTooManyRules,
		domain.ErrPermanentWithExpiry, domain.ErrExpiryRequired,
		domain.ErrExpiryInPast, domain.ErrCreatedByRequired:
		return apperr.BadRequest(err.Error())
	default:
		return apperr.Internal("feature flag validation failed", err)
	}
}
