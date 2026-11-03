package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// 邀请相关长度上限（与迁移 000014 列宽对齐）。
const (
	// MaxInvitationEmailLength 被邀请邮箱上限。
	MaxInvitationEmailLength = 128
	// inviteTokenBytes 明文 token 的随机字节数：256 bit 熵，不可枚举。
	inviteTokenBytes = 32
)

// DefaultInvitationTTL 邀请默认有效期：7 天。
//
// 过长会让离职人员的邀请成为悬空的成员后门；过短会造成大量 resend 噪声。
// 运营口径可在 Options 覆盖，这里是领域默认值。
const DefaultInvitationTTL = 7 * 24 * time.Hour

// InvitationStatus 是邀请的生命周期状态；迁移全部从 pending 出发。
type InvitationStatus string

// 邀请状态取值。
const (
	// InvitationPending 待处理：token 有效，可接受 / 撤销 / 重发。
	InvitationPending InvitationStatus = "pending"
	// InvitationAccepted 已接受：成员关系已建立，重复接受幂等返回。
	InvitationAccepted InvitationStatus = "accepted"
	// InvitationRevoked 已撤销：运营显式作废。
	InvitationRevoked InvitationStatus = "revoked"
	// InvitationExpired 已过期：接受时惰性判定并回写。
	InvitationExpired InvitationStatus = "expired"
)

// Valid 判断状态是否已定义。
func (s InvitationStatus) Valid() bool {
	switch s {
	case InvitationPending, InvitationAccepted, InvitationRevoked, InvitationExpired:
		return true
	default:
		return false
	}
}

// Terminal 判断是否为终态：终态邀请的 token 一律不可再接受。
func (s InvitationStatus) Terminal() bool {
	return s != InvitationPending
}

// Invitation 是一条成员邀请。
type Invitation struct {
	// ID 邀请 ID。
	ID string
	// TenantID 目标租户。
	TenantID string
	// Email 被邀请人邮箱；投递目标，T-14 阶段仅记录（ENT-10 就绪前不发送）。
	Email string
	// Role 受邀角色；接受时据此建立成员关系。
	Role MemberRole
	// TokenHash SHA-256(token) 十六进制摘要；明文永不落库。
	TokenHash string
	// Status 当前状态。
	Status InvitationStatus
	// InvitedBy 发起邀请的操作者主体。
	InvitedBy string
	// AcceptedBy 实际接受的主体（subject_id）；与 Email 解耦。
	AcceptedBy string
	// ExpiresAt 过期时间；接受时惰性校验并回写 expired。
	ExpiresAt time.Time
	// AcceptedAt 接受时间。
	AcceptedAt *time.Time
	// CreatedAt / UpdatedAt 时间戳。
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewInviteToken 生成一对（明文 token, 摘要）。
//
// 明文只走创建 / 重发的响应（HTTPS 下的一次性下发），库里只留摘要：
// 拿到库备份的人无法反推任何有效邀请。
func NewInviteToken() (plaintext, hash string, err error) {
	buf := make([]byte, inviteTokenBytes)
	if _, err = rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("tenant: generate invitation token: %w", err)
	}
	plaintext = base64.RawURLEncoding.EncodeToString(buf)
	return plaintext, HashInviteToken(plaintext), nil
}

// HashInviteToken 计算明文 token 的 SHA-256 十六进制摘要。
func HashInviteToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// NewInvitation 构造一条待处理邀请；邮箱 / 角色 / 租户即校验，摘要由调用方
// 通过 NewInviteToken 生成后传入（避免领域层与 token 形态耦合不必要）。
func NewInvitation(id, tenantID, email string, role MemberRole, invitedBy, tokenHash string,
	ttl time.Duration, now time.Time) (Invitation, error) {
	trimmedID := strings.TrimSpace(id)
	if trimmedID == "" {
		return Invitation{}, fmt.Errorf("tenant: invitation id is required")
	}
	if strings.TrimSpace(tenantID) == "" {
		return Invitation{}, fmt.Errorf("tenant: invitation tenant id is required")
	}
	normalizedEmail, err := NormalizeInvitationEmail(email)
	if err != nil {
		return Invitation{}, err
	}
	if !role.Valid() {
		return Invitation{}, &InvalidRoleError{Role: string(role)}
	}
	if strings.TrimSpace(tokenHash) == "" {
		return Invitation{}, fmt.Errorf("tenant: invitation token hash is required")
	}
	if ttl <= 0 {
		ttl = DefaultInvitationTTL
	}
	moment := now.UTC()
	return Invitation{
		ID:        trimmedID,
		TenantID:  strings.TrimSpace(tenantID),
		Email:     normalizedEmail,
		Role:      role,
		TokenHash: strings.TrimSpace(tokenHash),
		Status:    InvitationPending,
		InvitedBy: strings.TrimSpace(invitedBy),
		ExpiresAt: moment.Add(ttl),
		CreatedAt: moment,
		UpdatedAt: moment,
	}, nil
}

// NormalizeInvitationEmail 校验并规范化被邀请邮箱。
//
// 口径与租户联系人邮箱一致：含 @、无空白、限长——完整 RFC 校验交给
// 投递前的验证邮件（ENT-10），这里只拦「明显不可能投递」的输入。
func NormalizeInvitationEmail(email string) (string, error) {
	trimmed := strings.TrimSpace(email)
	if trimmed == "" {
		return "", ErrInvitationEmailRequired
	}
	if len([]rune(trimmed)) > MaxInvitationEmailLength {
		return "", ErrInvitationEmailTooLong
	}
	if strings.ContainsAny(trimmed, " \t\r\n") || !strings.Contains(trimmed, "@") {
		return "", &InvalidEmailError{Email: trimmed}
	}
	return trimmed, nil
}

// Validate 校验仓储回读的邀请自洽性。
func (i Invitation) Validate() error {
	if strings.TrimSpace(i.ID) == "" || strings.TrimSpace(i.TenantID) == "" {
		return fmt.Errorf("tenant: invitation id and tenant id are required")
	}
	if _, err := NormalizeInvitationEmail(i.Email); err != nil {
		return err
	}
	if !i.Role.Valid() {
		return &InvalidRoleError{Role: string(i.Role)}
	}
	if strings.TrimSpace(i.TokenHash) == "" {
		return fmt.Errorf("tenant: invitation token hash is required")
	}
	if !i.Status.Valid() {
		return fmt.Errorf("tenant: unsupported invitation status %q", i.Status)
	}
	if i.Status == InvitationAccepted && (i.AcceptedBy == "" || i.AcceptedAt == nil) {
		return fmt.Errorf("tenant: accepted invitation %s is missing acceptee", i.ID)
	}
	return nil
}

// Expired 判断邀请在给定时刻是否已过期。
func (i Invitation) Expired(now time.Time) bool {
	return now.UTC().After(i.ExpiresAt)
}
