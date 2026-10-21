package domain

import "time"

// MaxBreakglassDuration 破玻璃绑定的最大有效期（§8.5：默认上限 2 小时）。
const MaxBreakglassDuration = 2 * time.Hour

// Binding 是主体与角色的绑定关系聚合。
//
// 不变量：
//   - 端别、主体类型、商户三者必须自洽（A 端 staff 无商户，B 端 merchant_staff 必有商户）；
//   - 绑定只能指向同端别、同商户且处于 active 的角色；
//   - 破玻璃绑定必须有有效期，且不超过 MaxBreakglassDuration；
//   - 过期绑定不删除（保留审计线索），由查询条件过滤。
type Binding struct {
	// ID 雪花 ID。
	ID string
	// Channel 端别。
	Channel Channel
	// SubjectType 主体类型。
	SubjectType SubjectType
	// SubjectID 主体 ID。
	SubjectID string
	// MerchantID B 端商户 ID；A 端为空串。
	MerchantID string
	// RoleID 关联角色 ID。
	RoleID string
	// ExpiresAt 过期时间；nil 表示长期有效。
	ExpiresAt *time.Time
	// GrantedBy 授权人 subject_id。
	GrantedBy string
	// CreatedAt 绑定时间（UTC）。
	CreatedAt time.Time
}

// NewBindingParams 创建绑定的入参。
type NewBindingParams struct {
	ID          string
	Channel     Channel
	SubjectType SubjectType
	SubjectID   string
	MerchantID  string
	RoleID      string
	ExpiresAt   *time.Time
	GrantedBy   string
}

// NewBinding 构造并校验绑定自身字段（不含角色交叉校验）。
//
// 角色相关校验必须随后调用 ValidateAgainst，因为绑定合法性依赖目标角色状态；
// 分成两步可让 app 层在同一事务内先锁角色再校验，避免 TOCTOU。
func NewBinding(p NewBindingParams) (*Binding, error) {
	if p.ID == "" {
		return nil, errInvalid(ErrInvalidBinding, "binding id is required")
	}
	if p.SubjectID == "" {
		return nil, errInvalid(ErrInvalidBinding, "subject id is required")
	}
	if p.RoleID == "" {
		return nil, errInvalid(ErrInvalidBinding, "role id is required")
	}
	if p.GrantedBy == "" {
		return nil, errInvalid(ErrMissingActor, "binding granter is required")
	}
	if err := p.Channel.Validate(); err != nil {
		return nil, err
	}
	if err := p.SubjectType.Validate(); err != nil {
		return nil, err
	}

	switch p.Channel {
	case ChannelAdmin:
		if p.SubjectType != SubjectStaff {
			return nil, errInvalid(ErrInvalidSubjectType,
				"channel admin requires subject type staff, got %q", string(p.SubjectType))
		}
		if p.MerchantID != "" {
			return nil, errInvalid(ErrInvalidBinding, "channel admin binding must not carry merchant id")
		}
	case ChannelBiz:
		if p.SubjectType != SubjectMerchantStaff {
			return nil, errInvalid(ErrInvalidSubjectType,
				"channel biz requires subject type merchant_staff, got %q", string(p.SubjectType))
		}
		if p.MerchantID == "" {
			return nil, errInvalid(ErrInvalidBinding, "channel biz binding requires merchant id")
		}
	}

	now := nowUTC()
	var expiresAt *time.Time
	if p.ExpiresAt != nil {
		value := p.ExpiresAt.UTC()
		if !value.After(now) {
			return nil, errInvalid(ErrInvalidBinding, "binding expiry must be in the future")
		}
		expiresAt = &value
	}

	return &Binding{
		ID:          p.ID,
		Channel:     p.Channel,
		SubjectType: p.SubjectType,
		SubjectID:   p.SubjectID,
		MerchantID:  p.MerchantID,
		RoleID:      p.RoleID,
		ExpiresAt:   expiresAt,
		GrantedBy:   p.GrantedBy,
		CreatedAt:   now,
	}, nil
}

// IsExpired 报告绑定在 now 时刻是否已过期；无过期时间表示长期有效。
func (b *Binding) IsExpired(now time.Time) bool {
	if b.ExpiresAt == nil {
		return false
	}
	return !now.Before(*b.ExpiresAt)
}

// IsEffective 报告绑定在 now 时刻是否有效。
func (b *Binding) IsEffective(now time.Time) bool { return !b.IsExpired(now) }

// ValidateAgainst 校验绑定与目标角色的一致性（跨端 / 跨商户 / 状态 / 破玻璃时效）。
//
// 任何一个不匹配都必须拒绝：跨端绑定是直接的提权通道，
// 仅靠仓储查询条件过滤不足以保证写入侧安全。
func (b *Binding) ValidateAgainst(role *Role, now time.Time) error {
	if role == nil {
		return errInvalid(ErrInvalidBinding, "binding target role is required")
	}
	if role.IsDeleted() {
		return errConflict(ErrRoleDeleted, "role %s is deleted", role.ID)
	}
	if role.Channel != b.Channel {
		return errInvalid(ErrBindingChannelMismatch,
			"binding channel %q does not match role %s channel %q",
			string(b.Channel), role.ID, string(role.Channel))
	}
	if role.MerchantID != b.MerchantID {
		return errInvalid(ErrBindingChannelMismatch,
			"binding merchant %q does not match role %s merchant %q",
			b.MerchantID, role.ID, role.MerchantID)
	}
	if role.Status != RoleStatusActive {
		return errConflict(ErrRoleNotActive, "role %s is disabled", role.ID)
	}
	if role.Type != RoleTypeBreakglass {
		return nil
	}

	if b.ExpiresAt == nil {
		return errInvalid(ErrBreakglassRequiresExpiry,
			"breakglass binding to role %s requires an expiry", role.ID)
	}
	if !b.ExpiresAt.After(now) {
		return errInvalid(ErrBreakglassRequiresExpiry,
			"breakglass binding to role %s is already expired", role.ID)
	}
	if b.ExpiresAt.Sub(now) > MaxBreakglassDuration {
		return errInvalid(ErrBreakglassRequiresExpiry,
			"breakglass binding expiry must be within %s", MaxBreakglassDuration)
	}
	return nil
}
