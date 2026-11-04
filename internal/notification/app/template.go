package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// 模板相关审计动作与资源类型（docs/audit/EVENTS.md 的 notification.* 段）。
const (
	// ActionTemplateCreate 创建模板（新键首版本）。
	ActionTemplateCreate = "notification.template.create"
	// ActionTemplateUpdate 更新模板（同键新版本 / 停用 / 启用）。
	ActionTemplateUpdate = "notification.template.update"
	// ResourceTypeTemplate 模板资源类型。
	ResourceTypeTemplate = "notification_template"
)

// TemplateService 是模板管理用例：注册、版本化更新、停用与查询。
//
// 渲染只读（Render 供编排层复用），变更动作全部入审计——模板内容
// 决定客户看到什么，出问题必须能回答「谁在什么时候改的」。
type TemplateService struct {
	templates domain.TemplateRepository
	audit     auditContext
	ids       id.Generator
	now       func() time.Time
}

// NewTemplateService 构造模板用例；仓储 / ID 生成器 / 审计出口必填。
func NewTemplateService(templates domain.TemplateRepository, ids id.Generator,
	emitter audit.Emitter, opts Options) (*TemplateService, error) {
	switch {
	case templates == nil:
		return nil, apperr.Internal("notification template service requires a template repository", nil)
	case ids == nil:
		return nil, apperr.Internal("notification template service requires an id generator", nil)
	case emitter == nil:
		return nil, apperr.Internal("notification template service requires an audit emitter", nil)
	}
	now := time.Now
	return &TemplateService{
		templates: templates,
		audit:     auditContext{emitter: emitter, ids: ids, service: opts.Service, now: now, observer: observerOrNoop(opts.Observer)},
		ids:       ids,
		now:       now,
	}, nil
}

// UpsertTemplateInput 是注册 / 更新模板入参。
type UpsertTemplateInput struct {
	// Key 模板键。
	Key string
	// Channel 目标渠道。
	Channel domain.Channel
	// Locale 语言标签；空取默认 locale。
	Locale string
	// Subject 主题（email 必填）。
	Subject string
	// Body 正文 / 模板体。
	Body string
	// Actor 操作者。
	Actor Actor
}

// Upsert 注册模板：新键落 v1，已存在键落新版本（渲染按版本锁定）。
// 同事务语义由仓储保证（单行 upsert）；审计失败整体失败（fail closed）。
func (s *TemplateService) Upsert(ctx context.Context, in UpsertTemplateInput) (domain.Template, error) {
	locale := in.Locale
	if locale == "" {
		locale = "zh-CN"
	}
	tpl, err := domain.NewTemplate(in.Key, in.Channel, locale, in.Subject, in.Body, in.Actor.SubjectID, s.now())
	if err != nil {
		return domain.Template{}, translateIfNeeded(err)
	}
	saved, err := s.templates.Upsert(ctx, tpl)
	if err != nil {
		return domain.Template{}, apperr.Internal("save notification template failed", err)
	}
	action := ActionTemplateCreate
	if saved.Version > domain.InitialTemplateVersion {
		action = ActionTemplateUpdate
	}
	attrs := map[string]string{
		"template_key": saved.Key,
		"channel":      string(saved.Channel),
		"locale":       saved.Locale,
		"version":      strconv.FormatInt(saved.Version, 10),
	}
	if err := s.audit.emit(ctx, in.Actor, action, ResourceTypeTemplate, saved.Key,
		audit.OutcomeSuccess, "", attrs); err != nil {
		return domain.Template{}, apperr.Internal("emit template audit failed", err)
	}
	return saved, nil
}

// SetEnabled 启用 / 停用模板（停用的模板渲染直接 404）。
func (s *TemplateService) SetEnabled(ctx context.Context, key string, channel domain.Channel,
	locale string, enabled bool, actor Actor) error {
	tpl, err := s.templates.Find(ctx, key, channel, locale)
	if err != nil {
		if errors.Is(err, domain.ErrTemplateNotFound) {
			return apperr.NotFound("notification template not found")
		}
		return apperr.Internal("load notification template failed", err)
	}
	tpl.Enabled = enabled
	tpl.UpdatedAt = s.now().UTC()
	if _, err := s.templates.Upsert(ctx, tpl); err != nil {
		return apperr.Internal("save notification template failed", err)
	}
	attrs := map[string]string{
		"template_key": key,
		"channel":      string(channel),
		"locale":       locale,
		"enabled":      strconv.FormatBool(enabled),
	}
	if err := s.audit.emit(ctx, actor, ActionTemplateUpdate, ResourceTypeTemplate, key,
		audit.OutcomeSuccess, "", attrs); err != nil {
		return apperr.Internal("emit template audit failed", err)
	}
	return nil
}

// Find 供编排层定位启用中的最新版本模板（不审读，渲染前置步骤）。
func (s *TemplateService) Find(ctx context.Context, key string, channel domain.Channel, locale string) (domain.Template, error) {
	tpl, err := s.templates.Find(ctx, key, channel, locale)
	if err != nil {
		if errors.Is(err, domain.ErrTemplateNotFound) {
			return domain.Template{}, apperr.NotFound("notification template not found: " + key)
		}
		return domain.Template{}, apperr.Internal("load notification template failed", err)
	}
	return tpl, nil
}

// Render 用模板渲染参数（编排管道第 3 步复用）。
func (s *TemplateService) Render(ctx context.Context, key string, channel domain.Channel,
	locale string, params map[string]string) (domain.Rendered, error) {
	tpl, err := s.Find(ctx, key, channel, locale)
	if err != nil {
		return domain.Rendered{}, err
	}
	out, err := tpl.Render(params)
	if err != nil {
		return domain.Rendered{}, translateIfNeeded(err)
	}
	return out, nil
}

// List 按模板键列出版本历史（管理端查询）。
func (s *TemplateService) List(ctx context.Context, key string) ([]domain.Template, error) {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return nil, apperr.BadRequest("template key is required")
	}
	items, err := s.templates.List(ctx, trimmed)
	if err != nil {
		return nil, apperr.Internal("list notification templates failed", err)
	}
	return items, nil
}
