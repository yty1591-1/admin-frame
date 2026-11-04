package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// 偏好相关审计动作与资源类型。
const (
	// ActionPreferenceUpdate 变更渠道偏好（含退订）。
	ActionPreferenceUpdate = "notification.preference.update"
	// ResourceTypePreference 偏好资源类型。
	ResourceTypePreference = "notification_preference"
)

// PreferenceService 是渠道偏好用例：退订 / 恢复 / 查询。
//
// 命中语义：偏好存在且 Enabled=false → 拒发（rejected）；不存在 → 默认允许
// （opt-in 模型）。事务类场景的退订在领域层直接拒绝（账号安全必须可达）。
type PreferenceService struct {
	preferences domain.PreferenceRepository
	audit       auditContext
	ids         id.Generator
	now         func() time.Time
}

// NewPreferenceService 构造偏好用例。
func NewPreferenceService(preferences domain.PreferenceRepository, ids id.Generator,
	emitter audit.Emitter, opts Options) (*PreferenceService, error) {
	switch {
	case preferences == nil:
		return nil, apperr.Internal("notification preference service requires a preference repository", nil)
	case ids == nil:
		return nil, apperr.Internal("notification preference service requires an id generator", nil)
	case emitter == nil:
		return nil, apperr.Internal("notification preference service requires an audit emitter", nil)
	}
	now := time.Now
	return &PreferenceService{
		preferences: preferences,
		audit:       auditContext{emitter: emitter, ids: ids, service: opts.Service, now: now, observer: observerOrNoop(opts.Observer)},
		ids:         ids,
		now:         now,
	}, nil
}

// SetPreferenceInput 是偏好变更入参；enabled=false 即退订。
type SetPreferenceInput struct {
	// TenantID 租户隔离键。
	TenantID string
	// SubjectType 主体类型（user / tenant）。
	SubjectType domain.SubjectType
	// SubjectID 主体 ID。
	SubjectID string
	// SceneClass 场景分类。
	SceneClass domain.SceneClass
	// Channel 渠道。
	Channel domain.Channel
	// Enabled true=恢复接收；false=退订。
	Enabled bool
	// Actor 操作者（自助退订是用户本人；代操作入审计）。
	Actor Actor
}

// Set 变更偏好；变更动作入审计（退订必须可追溯到操作者与时间）。
func (s *PreferenceService) Set(ctx context.Context, in SetPreferenceInput) (domain.Preference, error) {
	p, err := domain.NewPreference(in.TenantID, in.SubjectType, in.SubjectID,
		in.SceneClass, in.Channel, in.Enabled, s.now())
	if err != nil {
		return domain.Preference{}, translateIfNeeded(err)
	}
	if err := s.preferences.Upsert(ctx, p); err != nil {
		return domain.Preference{}, apperr.Internal("save notification preference failed", err)
	}
	attrs := map[string]string{
		"subject_type": string(in.SubjectType),
		"subject_id":   in.SubjectID,
		"scene_class":  string(in.SceneClass),
		"channel":      string(in.Channel),
		"enabled":      enabledAttr(in.Enabled),
	}
	if err := s.audit.emit(ctx, in.Actor, ActionPreferenceUpdate, ResourceTypePreference,
		preferenceResourceID(in), audit.OutcomeSuccess, "", attrs); err != nil {
		return domain.Preference{}, apperr.Internal("emit preference audit failed", err)
	}
	return p, nil
}

// Get 查询单条偏好；不存在返回 ErrNotFound（调用方语义：默认允许）。
func (s *PreferenceService) Get(ctx context.Context, tenantID string, subjectType domain.SubjectType,
	subjectID string, class domain.SceneClass, channel domain.Channel) (domain.Preference, error) {
	p, err := s.preferences.Get(ctx, tenantID, subjectType, subjectID, class, channel)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Preference{}, apperr.NotFound("notification preference not found")
		}
		return domain.Preference{}, apperr.Internal("load notification preference failed", err)
	}
	return p, nil
}

// ListForSubject 列出主体全部偏好（退订页 / 管理端）。
func (s *PreferenceService) ListForSubject(ctx context.Context, tenantID string,
	subjectType domain.SubjectType, subjectID string) ([]domain.Preference, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(subjectID) == "" {
		return nil, apperr.BadRequest("tenant id and subject id are required")
	}
	items, err := s.preferences.ListForSubject(ctx, tenantID, subjectType, subjectID)
	if err != nil {
		return nil, apperr.Internal("list notification preferences failed", err)
	}
	return items, nil
}

// IsAllowed 是编排管道第 2 步的判定入口：偏好缺省视为允许。
//
// 返回 (allowed, nil)；偏好存在且停用 → (false, nil)。
func (s *PreferenceService) IsAllowed(ctx context.Context, tenantID string, subjectType domain.SubjectType,
	subjectID string, class domain.SceneClass, channel domain.Channel) (bool, error) {
	p, err := s.preferences.Get(ctx, tenantID, subjectType, subjectID, class, channel)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return true, nil
		}
		return false, apperr.Internal("load notification preference failed", err)
	}
	return p.Enabled, nil
}

func preferenceResourceID(in SetPreferenceInput) string {
	return in.TenantID + "/" + string(in.SubjectType) + "/" + in.SubjectID +
		"/" + string(in.SceneClass) + "/" + string(in.Channel)
}

func enabledAttr(enabled bool) string {
	if enabled {
		return "true"
	}
	return "false"
}
