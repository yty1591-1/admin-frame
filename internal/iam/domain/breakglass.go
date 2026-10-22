package domain

import (
	"strings"
	"time"
)

// BreakglassRoleCode 是系统内置破玻璃角色编码（A 端平台级）。
//
// 该角色由迁移 000004 播种，role_type=breakglass，且不持有任何权限点：
// 超级权限只在有效审批绑定存在时由快照动态展开为 "*"（docs/rbac-iam.md §8.5）。
const BreakglassRoleCode = "breakglass_admin"

// NewBreakglassGrantParams 是申请破玻璃授权的入参。
type NewBreakglassGrantParams struct {
	// Channel 端别；当前只允许 admin（B 端审批需 merchant_id 迁移）。
	Channel Channel
	// SubjectType 主体类型，必须与端别自洽。
	SubjectType SubjectType
	// SubjectID 主体 ID。
	SubjectID string
	// MerchantID B 端商户 ID；A 端必须为空串。
	MerchantID string
	// Duration 授权时长，必须落在 (0, MaxBreakglassDuration]。
	Duration time.Duration
	// Now 基准时间；零值取当前 UTC 时间（测试可注入）。
	Now time.Time
}

// BreakglassGrant 是审批通过后要创建的临时授权（值对象，不落独立表）。
//
// 生效方式是一条带 expires_at 的绑定；到期后快照查询自动过滤，无需后台任务。
type BreakglassGrant struct {
	// Channel 端别。
	Channel Channel
	// SubjectType 主体类型。
	SubjectType SubjectType
	// SubjectID 主体 ID。
	SubjectID string
	// MerchantID B 端商户 ID；A 端为空串。
	MerchantID string
	// ExpiresAt 绝对过期时间（UTC）。
	ExpiresAt time.Time
}

// NewBreakglassGrant 校验主体、端别与时长，并把相对时长折算为绝对过期时间。
//
// 时长上限硬编码在领域层（MaxBreakglassDuration），不依赖配置：
// 即使配置被误改，也不能产生超过 2 小时的超级权限。
func NewBreakglassGrant(p NewBreakglassGrantParams) (BreakglassGrant, error) {
	if err := p.Channel.Validate(); err != nil {
		return BreakglassGrant{}, err
	}
	if err := p.SubjectType.Validate(); err != nil {
		return BreakglassGrant{}, err
	}
	if strings.TrimSpace(p.SubjectID) == "" {
		return BreakglassGrant{}, errInvalid(ErrEmptyInput, "breakglass subject id is required")
	}
	switch p.Channel {
	case ChannelAdmin:
		if p.SubjectType != SubjectStaff {
			return BreakglassGrant{}, errInvalid(ErrInvalidSubjectType,
				"channel admin requires subject type staff, got %q", string(p.SubjectType))
		}
		if p.MerchantID != "" {
			return BreakglassGrant{}, errInvalid(ErrInvalidBinding,
				"channel admin breakglass must not carry merchant id")
		}
	case ChannelBiz:
		if p.SubjectType != SubjectMerchantStaff {
			return BreakglassGrant{}, errInvalid(ErrInvalidSubjectType,
				"channel biz requires subject type merchant_staff, got %q", string(p.SubjectType))
		}
		if p.MerchantID == "" {
			return BreakglassGrant{}, errInvalid(ErrInvalidBinding,
				"channel biz breakglass requires merchant id")
		}
	}

	if p.Duration <= 0 {
		return BreakglassGrant{}, errInvalid(ErrBreakglassRequiresExpiry,
			"breakglass duration must be positive")
	}
	if p.Duration > MaxBreakglassDuration {
		return BreakglassGrant{}, errInvalid(ErrBreakglassRequiresExpiry,
			"breakglass duration must be within %s", MaxBreakglassDuration)
	}

	now := p.Now
	if now.IsZero() {
		now = nowUTC()
	}
	return BreakglassGrant{
		Channel:     p.Channel,
		SubjectType: p.SubjectType,
		SubjectID:   p.SubjectID,
		MerchantID:  p.MerchantID,
		ExpiresAt:   now.UTC().Add(p.Duration),
	}, nil
}

// BindingExpiry 返回绑定所需的过期时间指针（每次调用返回新地址）。
func (g BreakglassGrant) BindingExpiry() *time.Time {
	value := g.ExpiresAt.UTC()
	return &value
}
