package app

// 本文件只承载认证用例的入参 / 出参 DTO（MNT-02）。
//
// 约定：
//   - DTO 不依赖具体仓储或传输层，便于在 transport 与用例之间稳定复用；
//   - 对外视图只包含展示所需字段，禁止携带令牌、手机号等 PII 原文。

import (
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
)

// LoginInput 是密码登录入参。
type LoginInput struct {
	// Subject 登录主体（A/B 端为账号 / 邮箱）。
	Subject string
	// MerchantID B 端商户 ID；B 端必填，A/C 端忽略。
	MerchantID string
	// Password 明文密码；只在本层短暂存在，禁止写日志。
	Password string
	// IPHash / UAHash 客户端摘要；由 transport 计算。
	IPHash string
	UAHash string
	// Device 可展示设备画像；由 transport 从原始 UA / IP 派生，仅用于会话展示。
	Device domain.DeviceInfo
	// TraceID 请求关联 ID。
	TraceID string
}

// LoginResult 是登录成功后的令牌与会话信息。
type LoginResult struct {
	TokenPair
	// SessionID 本次登录创建的会话。
	SessionID string
	// AccountID 账号主键。
	AccountID string
	// MFARequired 表示密码已通过、但需要完成 MFA 才能获得令牌。
	MFARequired bool
	// MFATicket 是两步登录票据；仅在 MFARequired 时返回。
	MFATicket string
	// MFABindingRequired 表示密码已通过、但账号尚未绑定 MFA 且 A 端强制绑定（§7.2.1）。
	MFABindingRequired bool
	// MFASetupTicket 是强制绑定票据；仅在 MFABindingRequired 时返回。
	MFASetupTicket string
	// PasswordChangeRequired 表示管理员重置密码后必须先完成改密。
	PasswordChangeRequired bool
	// PasswordChangeTicket 是强制改密票据；仅在 PasswordChangeRequired 时返回。
	PasswordChangeTicket string
	// activatedSession 是成功创建会话时的撤销快照，仅供提交后写缓存，不对外暴露。
	activatedSession *SessionState
}

// RefreshInput 是刷新令牌入参。
type RefreshInput struct {
	RefreshToken string
	TraceID      string
	// Device 当次刷新请求派生出的画像；仅用于补齐历史会话缺失的设备信息。
	Device domain.DeviceInfo
}

// SessionActor 是需要认证上下文的用例入参。
type SessionActor struct {
	AccountID      string
	SessionID      string
	TokenID        string
	TokenExpiresAt time.Time
	MerchantID     string
	TraceID        string
}

// SessionView 是会话列表的对外视图；不包含任何令牌信息。
type SessionView struct {
	ID         string
	Current    bool
	Status     string
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
	// AbsoluteExpiresAt 设备绝对到期时间；刷新不得延长。
	AbsoluteExpiresAt time.Time
	IdleExpiresAt     time.Time
	RevokedAt         *time.Time
	// DeviceLabel / DeviceType / OS / Browser / IPMasked 设备画像；供前端展示设备。
	DeviceLabel string
	DeviceType  string
	OS          string
	Browser     string
	IPMasked    string
}

// MeView 是当前登录主体资料的对外视图。
//
// 只承载身份域字段；角色、权限与数据范围仍由 IAM 的 /iam/me/permissions 提供。
type MeView struct {
	// SubjectID 账号主键，与令牌 sub 一致。
	SubjectID string
	// Subject 规范化登录主体（trim + 小写）。
	Subject string
	// DisplayName 展示昵称；空串表示未设置，前端回退 Subject。
	DisplayName string
	// Channel 账号所属端别；必须与装配端别一致。
	Channel domain.Channel
	// MerchantID B 端商户 ID；A / C 端为空串。
	MerchantID string
	// Status 账号生命周期状态。
	Status domain.AccountStatus
	// MFAEnabled 表示 MFA 绑定是否已确认。
	MFAEnabled bool
	// HasPassword 表示账号已设置密码凭据。
	HasPassword bool
	// PasswordUpdatedAt 最近一次改密时间；nil 表示从未设置密码或从未改密。
	PasswordUpdatedAt *time.Time
	// MustChangePassword 表示管理员重置密码后尚未完成改密。
	MustChangePassword bool
	// PhoneBound 表示账号已绑定手机号凭据；号码本身不可回显。
	PhoneBound bool
	// TokenVersion 账号级撤销版本，只读。
	TokenVersion int64
}
