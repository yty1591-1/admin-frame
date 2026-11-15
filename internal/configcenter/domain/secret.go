package domain

import (
	"fmt"
	"strings"
	"time"
)

// SecretStatus 是业务密钥治理状态（ADR-0014 / 计划 §6.5）。
type SecretStatus string

// 密钥治理状态枚举。
const (
	SecretStatusDraft          SecretStatus = "draft"
	SecretStatusPendingReview  SecretStatus = "pending_review"
	SecretStatusActive         SecretStatus = "active"
	SecretStatusRejected       SecretStatus = "rejected"
	SecretStatusRevoked        SecretStatus = "revoked"
	SecretStatusDestroyed      SecretStatus = "destroyed"
)

// Valid 判断密钥状态枚举合法性。
func (s SecretStatus) Valid() bool {
	switch s {
	case SecretStatusDraft, SecretStatusPendingReview, SecretStatusActive,
		SecretStatusRejected, SecretStatusRevoked, SecretStatusDestroyed:
		return true
	default:
		return false
	}
}

// SecretVersionStatus 是密钥版本状态。
type SecretVersionStatus string

// 版本状态枚举。
const (
	SecretVersionActive    SecretVersionStatus = "active"
	SecretVersionGrace     SecretVersionStatus = "grace"
	SecretVersionRevoked   SecretVersionStatus = "revoked"
	SecretVersionDestroyed SecretVersionStatus = "destroyed"
)

// Valid 判断版本状态合法性。
func (s SecretVersionStatus) Valid() bool {
	switch s {
	case SecretVersionActive, SecretVersionGrace, SecretVersionRevoked, SecretVersionDestroyed:
		return true
	default:
		return false
	}
}

// SecretReviewAction 是密钥审批动作。
type SecretReviewAction string

// 审批动作枚举。
const (
	SecretReviewCreate   SecretReviewAction = "create"
	SecretReviewRotate   SecretReviewAction = "rotate"
	SecretReviewRevoke   SecretReviewAction = "revoke"
	SecretReviewGrant    SecretReviewAction = "grant"
	SecretReviewActivate SecretReviewAction = "activate"
)

// Secret 是业务密钥元数据；不含明文 / 密文。
type Secret struct {
	ID                 int64
	Env                string
	Namespace          string
	Name               string
	Description        string
	Owner              string
	Status             SecretStatus
	ActiveVersion      *int64
	Version            int64
	RotateIntervalDays int
	DualVersion        bool
	SubmittedBy        string
	ExpiresAt          *time.Time
	CreatedBy          string
	UpdatedBy          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	// Fingerprint 是当前 active 版本指纹；仓储列表可不填。
	Fingerprint string
}

// Validate 校验密钥元数据一致性。
func (s Secret) Validate() error {
	if !ValidEnv(s.Env) {
		return fmt.Errorf("configcenter: invalid secret env %q", s.Env)
	}
	if !ValidNamespace(s.Namespace) {
		return fmt.Errorf("configcenter: invalid secret namespace %q", s.Namespace)
	}
	if !ValidKey(s.Name) {
		return fmt.Errorf("configcenter: invalid secret name %q", s.Name)
	}
	if !s.Status.Valid() {
		return fmt.Errorf("configcenter: invalid secret status %q", s.Status)
	}
	if strings.TrimSpace(s.Owner) == "" || len(s.Owner) > MaxOwnerLength {
		return fmt.Errorf("configcenter: secret owner is required and must not exceed %d bytes", MaxOwnerLength)
	}
	if len(s.Description) > MaxDescriptionLength {
		return fmt.Errorf("configcenter: secret description must not exceed %d bytes", MaxDescriptionLength)
	}
	if s.RotateIntervalDays < 0 {
		return fmt.Errorf("configcenter: rotate_interval_days must be >= 0")
	}
	if s.Version < 1 {
		return fmt.Errorf("configcenter: secret version must be >= 1")
	}
	return nil
}

// SecretVersion 是信封加密后的密钥版本；密文列仅 persistence 层读写。
type SecretVersion struct {
	ID          int64
	SecretID    int64
	Version     int64
	Ciphertext  []byte
	Nonce       []byte
	WrappedDEK  []byte
	KEKKeyID    string
	Algorithm   string
	ValueHash   string
	Status      SecretVersionStatus
	CreatedBy   string
	CreatedAt   time.Time
	RevokedAt   *time.Time
	DestroyedAt *time.Time
}

// Validate 校验版本信封字段完整性（不做解密）。
func (v SecretVersion) Validate() error {
	if v.SecretID == 0 || v.Version < 1 {
		return fmt.Errorf("configcenter: secret version identity incomplete")
	}
	if len(v.Ciphertext) == 0 || len(v.Nonce) != 12 || len(v.WrappedDEK) == 0 {
		return fmt.Errorf("configcenter: secret version envelope incomplete")
	}
	if strings.TrimSpace(v.KEKKeyID) == "" || strings.TrimSpace(v.Algorithm) == "" {
		return fmt.Errorf("configcenter: secret version kek/algorithm required")
	}
	if len(v.ValueHash) != 16 {
		return fmt.Errorf("configcenter: secret version fingerprint must be 16 hex chars")
	}
	if !v.Status.Valid() {
		return fmt.Errorf("configcenter: invalid secret version status %q", v.Status)
	}
	return nil
}

// SecretGrant 是服务解析授权。
type SecretGrant struct {
	ID              int64
	Env             string
	Namespace       string
	ServiceIdentity string
	SecretID        int64
	Permission      string
	ExpiresAt       *time.Time
	CreatedBy       string
	CreatedAt       time.Time
	RevokedAt       *time.Time
}

// Validate 校验授权；禁止通配符。
func (g SecretGrant) Validate() error {
	if !ValidEnv(g.Env) || !ValidNamespace(g.Namespace) {
		return fmt.Errorf("configcenter: invalid grant env/namespace")
	}
	id := strings.TrimSpace(g.ServiceIdentity)
	if id == "" || strings.ContainsAny(id, "*?") {
		return fmt.Errorf("configcenter: service_identity required and must not contain wildcards")
	}
	if len(id) > 256 {
		return fmt.Errorf("configcenter: service_identity too long")
	}
	if g.Permission != "resolve" {
		return fmt.Errorf("configcenter: grant permission must be resolve")
	}
	if g.SecretID == 0 {
		return fmt.Errorf("configcenter: grant secret_id required")
	}
	return nil
}

// Active 判断授权当前是否有效。
func (g SecretGrant) Active(now time.Time) bool {
	if g.RevokedAt != nil {
		return false
	}
	if g.ExpiresAt != nil && !g.ExpiresAt.After(now) {
		return false
	}
	return true
}

// SecretReview 是密钥审批记录。
type SecretReview struct {
	ID        int64
	SecretID  int64
	Action    SecretReviewAction
	Reviewer  string
	Decision  ReviewDecision
	Comment   string
	CreatedAt time.Time
}

// SecretAccess 是解析访问审计摘要（可来自 audit 查询投影）。
type SecretAccess struct {
	OccurredAt      time.Time
	ServiceIdentity string
	SecretName      string
	SecretVersion   int64
	Result          string
	TraceID         string
}

// CanSubmit 判断是否可送审。
func (s Secret) CanSubmit() bool {
	return s.Status == SecretStatusDraft || s.Status == SecretStatusRejected
}

// CanApprove 判断是否可审批。
func (s Secret) CanApprove() bool {
	return s.Status == SecretStatusPendingReview
}

// CanRotate 判断是否可轮换。
func (s Secret) CanRotate() bool {
	return s.Status == SecretStatusActive
}

// CanRevoke 判断是否可吊销。
func (s Secret) CanRevoke() bool {
	return s.Status == SecretStatusActive || s.Status == SecretStatusPendingReview
}
