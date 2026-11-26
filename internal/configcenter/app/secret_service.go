package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/envelope"
	"github.com/admin-frame/admin-frame/pkg/id"
)

const (
	// ResourceTypeSecret 是密钥审计资源类型。
	ResourceTypeSecret = "config_secret"
	// ResourceTypeGrant 是密钥授权审计资源类型。
	ResourceTypeGrant = "config_secret_grant"
)

// SecretResolveObserver 观测密钥解析结果（低基数；不含明文）。
type SecretResolveObserver interface {
	ObserveSecretResolve(result string, elapsed time.Duration)
	ObserveSecretPlaintextLeak()
}

// SecretService 是业务密钥管理与解析用例。
type SecretService struct {
	secrets  domain.SecretRepository
	grants   domain.SecretGrantRepository
	access   domain.SecretAccessRepository
	kek      envelope.KEKProvider
	specs    *spec.Registry
	audit    auditContext
	ids      id.Generator
	now      func() time.Time
	observer SecretResolveObserver
	// LeaseTTL 默认租约；上限 15m。
	LeaseTTL time.Duration
}

// NewSecretService 构造密钥用例；kek 仅用于 wrap（管理面）或 unwrap（config-api）。
func NewSecretService(
	secrets domain.SecretRepository,
	grants domain.SecretGrantRepository,
	access domain.SecretAccessRepository,
	kek envelope.KEKProvider,
	emitter audit.Emitter,
	ids id.Generator,
	opts Options,
) (*SecretService, error) {
	switch {
	case secrets == nil:
		return nil, apperr.Internal("secret service requires a repository", nil)
	case grants == nil:
		return nil, apperr.Internal("secret service requires a grant repository", nil)
	case kek == nil:
		return nil, apperr.Internal("secret service requires a kek provider", nil)
	case emitter == nil:
		return nil, apperr.Internal("secret service requires an audit emitter", nil)
	case ids == nil:
		return nil, apperr.Internal("secret service requires an id generator", nil)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &SecretService{
		secrets:  secrets,
		grants:   grants,
		access:   access,
		kek:      kek,
		specs:    opts.Specs,
		audit:    auditContext{emitter: emitter, ids: ids, service: opts.Service, now: now},
		ids:      ids,
		now:      now,
		observer: opts.SecretObserver,
		LeaseTTL: 5 * time.Minute,
	}, nil
}

// enforceSecretRule 按 Registry 中的 SecretRule 校验明文（CC-R-14）。
//
// Specs 未注入或键未声明 Sensitive 时放行（兼容手工密钥名）；已声明则必须匹配。
func (s *SecretService) enforceSecretRule(namespace, name, plaintext string) error {
	if s.specs == nil {
		return nil
	}
	keySpec, ok := s.specs.Lookup(namespace, name)
	if !ok || !keySpec.Sensitive {
		return nil
	}
	if keySpec.SecretRule == nil {
		return apperr.BadRequest(fmt.Sprintf("sensitive key %s.%s missing secret_rule", namespace, name))
	}
	if err := keySpec.SecretRule.MatchPlaintext(plaintext); err != nil {
		return apperr.BadRequest(err.Error())
	}
	return nil
}

// requireAudit 写审计；失败则返回错误（CC-R-14 fail-closed，对齐 Item/Release）。
func (s *SecretService) requireAudit(ctx context.Context, actor Actor, action, resourceType, resourceID string,
	outcome audit.Outcome, reason string, attrs map[string]string) error {
	if err := s.audit.emit(ctx, actor, action, resourceType, resourceID, outcome, reason, attrs); err != nil {
		return apperr.Internal("emit config secret audit failed", err)
	}
	return nil
}

// CreateSecretInput 创建密钥入参。
type CreateSecretInput struct {
	Env                string
	Namespace          string
	Name               string
	Description        string
	Owner              string
	Plaintext          string
	DualVersion        bool
	RotateIntervalDays int
	ExpiresAt          *time.Time
	Actor              Actor
}

// CreateSecret 创建 draft 密钥并写入版本 1 密文；响应不含明文。
func (s *SecretService) CreateSecret(ctx context.Context, in CreateSecretInput) (domain.Secret, error) {
	if err := requireActor(in.Actor); err != nil {
		return domain.Secret{}, err
	}
	if strings.TrimSpace(in.Plaintext) == "" {
		return domain.Secret{}, apperr.BadRequest("plaintext is required")
	}
	if err := s.enforceSecretRule(in.Namespace, in.Name, in.Plaintext); err != nil {
		return domain.Secret{}, err
	}
	if in.RotateIntervalDays == 0 {
		in.RotateIntervalDays = 90
	}
	secretID, err := s.ids.Next()
	if err != nil {
		return domain.Secret{}, apperr.Internal("generate secret id failed", err)
	}
	versionID, err := s.ids.Next()
	if err != nil {
		return domain.Secret{}, apperr.Internal("generate secret version id failed", err)
	}
	now := s.now().UTC()
	aad := envelope.AAD(in.Env, in.Namespace, in.Name, 1)
	blob, err := envelope.Seal(s.kek, []byte(in.Plaintext), aad)
	if err != nil {
		return domain.Secret{}, apperr.Internal("encrypt secret failed", err)
	}
	active := int64(1)
	secret := domain.Secret{
		ID: secretID, Env: in.Env, Namespace: in.Namespace, Name: in.Name,
		Description: in.Description, Owner: in.Owner, Status: domain.SecretStatusDraft,
		ActiveVersion: &active, Version: 1, RotateIntervalDays: in.RotateIntervalDays,
		DualVersion: in.DualVersion, ExpiresAt: in.ExpiresAt,
		CreatedBy: in.Actor.SubjectID, UpdatedBy: in.Actor.SubjectID,
		CreatedAt: now, UpdatedAt: now, Fingerprint: blob.ValueHash,
	}
	if err := secret.Validate(); err != nil {
		return domain.Secret{}, apperr.BadRequest(err.Error())
	}
	version := domain.SecretVersion{
		ID: versionID, SecretID: secretID, Version: 1,
		Ciphertext: blob.Ciphertext, Nonce: blob.Nonce, WrappedDEK: blob.WrappedDEK,
		KEKKeyID: blob.KEKKeyID, Algorithm: blob.Algorithm, ValueHash: blob.ValueHash,
		Status: domain.SecretVersionActive, CreatedBy: in.Actor.SubjectID, CreatedAt: now,
	}
	if err := version.Validate(); err != nil {
		return domain.Secret{}, apperr.BadRequest(err.Error())
	}
	if err := s.secrets.Create(ctx, secret, version); err != nil {
		if errors.Is(err, domain.ErrDuplicateKey) {
			return domain.Secret{}, apperr.Conflict("secret already exists")
		}
		return domain.Secret{}, apperr.Internal("create secret failed", err)
	}
	if err := s.requireAudit(ctx, in.Actor, audit.ActionConfigSecretCreate, ResourceTypeSecret,
		strconv.FormatInt(secretID, 10), audit.OutcomeSuccess, "", map[string]string{
			"env": in.Env, "namespace": in.Namespace, "name": in.Name,
			"version": "1", "fingerprint": blob.ValueHash,
		}); err != nil {
		return domain.Secret{}, err
	}
	return secret, nil
}

// SubmitSecret 送审：draft/rejected → pending_review。
//
// 超级管理员提交后自动自批，并写 sod_bypass 审计；普通主体仍须另一人审批。
func (s *SecretService) SubmitSecret(ctx context.Context, secretID int64, actor Actor) (domain.Secret, error) {
	if err := requireActor(actor); err != nil {
		return domain.Secret{}, err
	}
	secret, err := s.secrets.GetByID(ctx, secretID)
	if err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	if !secret.CanSubmit() {
		return domain.Secret{}, apperr.Conflict("secret cannot be submitted in current status")
	}
	expected := secret.Version
	secret.Status = domain.SecretStatusPendingReview
	secret.SubmittedBy = actor.SubjectID
	secret.UpdatedBy = actor.SubjectID
	secret.UpdatedAt = s.now().UTC()
	secret.Version++
	if err := s.secrets.UpdateCAS(ctx, secret, expected); err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	if !actor.IsSystemAdmin() {
		return secret, nil
	}
	return s.ApproveSecret(ctx, ApproveSecretInput{
		ID:       secret.ID,
		Decision: domain.DecisionApproved,
		Comment:  "system_admin: skip dual approval",
		Actor:    actor,
	})
}

// ApproveSecretInput 审批入参。
type ApproveSecretInput struct {
	ID       int64
	Decision domain.ReviewDecision
	Comment  string
	Actor    Actor
}

// ApproveSecret 双人审批；提交人不得自批。覆盖创建 / 轮换 / 吊销 / 授权。
func (s *SecretService) ApproveSecret(ctx context.Context, in ApproveSecretInput) (domain.Secret, error) {
	if err := requireActor(in.Actor); err != nil {
		return domain.Secret{}, err
	}
	if in.Decision != domain.DecisionApproved && in.Decision != domain.DecisionRejected {
		return domain.Secret{}, apperr.BadRequest("decision must be approved or rejected")
	}
	secret, err := s.secrets.GetByID(ctx, in.ID)
	if err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	if !secret.CanApprove() {
		return domain.Secret{}, apperr.Conflict("secret is not pending review")
	}
	selfReview := secret.SubmittedBy == in.Actor.SubjectID
	if selfReview && !in.Actor.IsSystemAdmin() {
		return domain.Secret{}, apperr.Forbidden("submitter cannot review their own secret change")
	}
	if secret.PendingAction != "" {
		return s.approvePendingChange(ctx, secret, in)
	}
	expected := secret.Version
	now := s.now().UTC()
	if in.Decision == domain.DecisionApproved {
		secret.Status = domain.SecretStatusActive
	} else {
		secret.Status = domain.SecretStatusRejected
	}
	secret.UpdatedBy = in.Actor.SubjectID
	secret.UpdatedAt = now
	secret.Version++
	if err := s.secrets.UpdateCAS(ctx, secret, expected); err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	reviewID, _ := s.ids.Next()
	_ = s.secrets.AppendReview(ctx, domain.SecretReview{
		ID: reviewID, SecretID: secret.ID, Action: domain.SecretReviewCreate,
		Reviewer: in.Actor.SubjectID, Decision: in.Decision, Comment: in.Comment, CreatedAt: now,
	})
	attrs := map[string]string{"name": secret.Name, "decision": string(in.Decision)}
	if selfReview && in.Actor.IsSystemAdmin() {
		attrs["sod_bypass"] = RoleCodeSystemAdmin
	}
	if err := s.requireAudit(ctx, in.Actor, audit.ActionConfigSecretCreate, ResourceTypeSecret,
		strconv.FormatInt(secret.ID, 10), audit.OutcomeSuccess, string(in.Decision), attrs); err != nil {
		return domain.Secret{}, err
	}
	return secret, nil
}

func (s *SecretService) approvePendingChange(ctx context.Context, secret domain.Secret, in ApproveSecretInput) (domain.Secret, error) {
	now := s.now().UTC()
	action := secret.PendingAction
	reviewAction := domain.SecretReviewAction(action)
	if in.Decision == domain.DecisionRejected {
		if err := s.rejectPendingChange(ctx, secret, now); err != nil {
			return domain.Secret{}, err
		}
		expected := secret.Version
		secret.PendingAction = ""
		secret.PendingPayload = ""
		secret.SubmittedBy = ""
		secret.UpdatedBy = in.Actor.SubjectID
		secret.UpdatedAt = now
		secret.Version++
		if err := s.secrets.UpdateCAS(ctx, secret, expected); err != nil {
			return domain.Secret{}, mapSecretErr(err)
		}
		reviewID, _ := s.ids.Next()
		_ = s.secrets.AppendReview(ctx, domain.SecretReview{
			ID: reviewID, SecretID: secret.ID, Action: reviewAction,
			Reviewer: in.Actor.SubjectID, Decision: in.Decision, Comment: in.Comment, CreatedAt: now,
		})
		return secret, nil
	}
	switch action {
	case domain.PendingActionRotate:
		if err := s.applyPendingRotate(ctx, &secret, now, in.Actor); err != nil {
			return domain.Secret{}, err
		}
	case domain.PendingActionRevoke:
		if err := s.applyRevoke(ctx, &secret, now, in.Actor, in.Comment); err != nil {
			return domain.Secret{}, err
		}
	case domain.PendingActionGrant:
		if err := s.applyPendingGrant(ctx, &secret); err != nil {
			return domain.Secret{}, err
		}
	default:
		return domain.Secret{}, apperr.Conflict("unknown pending action")
	}
	expected := secret.Version
	secret.PendingAction = ""
	secret.PendingPayload = ""
	secret.SubmittedBy = ""
	secret.UpdatedBy = in.Actor.SubjectID
	secret.UpdatedAt = now
	secret.Version++
	if err := s.secrets.UpdateCAS(ctx, secret, expected); err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	reviewID, _ := s.ids.Next()
	_ = s.secrets.AppendReview(ctx, domain.SecretReview{
		ID: reviewID, SecretID: secret.ID, Action: reviewAction,
		Reviewer: in.Actor.SubjectID, Decision: in.Decision, Comment: in.Comment, CreatedAt: now,
	})
	return secret, nil
}

func (s *SecretService) rejectPendingChange(ctx context.Context, secret domain.Secret, now time.Time) error {
	switch secret.PendingAction {
	case domain.PendingActionRotate:
		pending, err := s.findPendingVersion(ctx, secret.ID)
		if err != nil {
			return mapSecretErr(err)
		}
		return mapSecretErr(s.secrets.UpdateVersionStatus(ctx, secret.ID, pending.Version,
			domain.SecretVersionPending, domain.SecretVersionDestroyed, now))
	case domain.PendingActionGrant:
		grantID, err := strconv.ParseInt(secret.PendingPayload, 10, 64)
		if err != nil {
			return apperr.Internal("invalid pending grant id", err)
		}
		return mapSecretErr(s.grants.Revoke(ctx, grantID, now))
	case domain.PendingActionRevoke:
		return nil
	default:
		return apperr.Conflict("unknown pending action")
	}
}

func (s *SecretService) applyPendingRotate(ctx context.Context, secret *domain.Secret, now time.Time, actor Actor) error {
	pending, err := s.findPendingVersion(ctx, secret.ID)
	if err != nil {
		return mapSecretErr(err)
	}
	oldVersion := int64(0)
	if secret.ActiveVersion != nil {
		oldVersion = *secret.ActiveVersion
	}
	if err := s.secrets.UpdateVersionStatus(ctx, secret.ID, pending.Version,
		domain.SecretVersionPending, domain.SecretVersionActive, now); err != nil {
		return mapSecretErr(err)
	}
	if oldVersion > 0 {
		_ = s.secrets.UpdateVersionStatus(ctx, secret.ID, oldVersion,
			domain.SecretVersionActive, domain.SecretVersionGrace, now)
	}
	secret.ActiveVersion = &pending.Version
	secret.Fingerprint = pending.ValueHash
	return s.requireAudit(ctx, actor, audit.ActionConfigSecretRotate, ResourceTypeSecret,
		strconv.FormatInt(secret.ID, 10), audit.OutcomeSuccess, "approved", map[string]string{
			"name": secret.Name, "old_version": strconv.FormatInt(oldVersion, 10),
			"new_version": strconv.FormatInt(pending.Version, 10), "fingerprint": pending.ValueHash,
		})
}

func (s *SecretService) applyRevoke(ctx context.Context, secret *domain.Secret, now time.Time, actor Actor, comment string) error {
	if secret.ActiveVersion != nil {
		_ = s.secrets.UpdateVersionStatus(ctx, secret.ID, *secret.ActiveVersion,
			domain.SecretVersionActive, domain.SecretVersionRevoked, now)
	}
	secret.Status = domain.SecretStatusRevoked
	return s.requireAudit(ctx, actor, audit.ActionConfigSecretRevoke, ResourceTypeSecret,
		strconv.FormatInt(secret.ID, 10), audit.OutcomeSuccess, comment, map[string]string{
			"name": secret.Name,
		})
}

func (s *SecretService) applyPendingGrant(ctx context.Context, secret *domain.Secret) error {
	grantID, err := strconv.ParseInt(secret.PendingPayload, 10, 64)
	if err != nil {
		return apperr.Internal("invalid pending grant id", err)
	}
	return mapSecretErr(s.grants.Activate(ctx, grantID))
}

func (s *SecretService) findPendingVersion(ctx context.Context, secretID int64) (domain.SecretVersion, error) {
	versions, _, err := s.secrets.ListVersions(ctx, secretID, 50, 0)
	if err != nil {
		return domain.SecretVersion{}, err
	}
	for _, meta := range versions {
		if meta.Status == domain.SecretVersionPending {
			return s.secrets.GetVersion(ctx, secretID, meta.Version)
		}
	}
	return domain.SecretVersion{}, domain.ErrNotFound
}

// RotateSecretInput 轮换入参。
type RotateSecretInput struct {
	ID        int64
	Plaintext string
	Comment   string
	Actor     Actor
}

// RotateSecret 提交轮换：写入 pending 版本，待双人审批后再生效。
func (s *SecretService) RotateSecret(ctx context.Context, in RotateSecretInput) (domain.Secret, error) {
	if err := requireActor(in.Actor); err != nil {
		return domain.Secret{}, err
	}
	if strings.TrimSpace(in.Plaintext) == "" {
		return domain.Secret{}, apperr.BadRequest("plaintext is required")
	}
	secret, err := s.secrets.GetByID(ctx, in.ID)
	if err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	if err := s.enforceSecretRule(secret.Namespace, secret.Name, in.Plaintext); err != nil {
		return domain.Secret{}, err
	}
	if !secret.CanRotate() {
		return domain.Secret{}, apperr.Conflict("secret cannot be rotated in current status")
	}
	oldVersion := int64(0)
	if secret.ActiveVersion != nil {
		oldVersion = *secret.ActiveVersion
	}
	newVersionNo := oldVersion + 1
	now := s.now().UTC()
	aad := envelope.AAD(secret.Env, secret.Namespace, secret.Name, newVersionNo)
	blob, err := envelope.Seal(s.kek, []byte(in.Plaintext), aad)
	if err != nil {
		return domain.Secret{}, apperr.Internal("encrypt rotated secret failed", err)
	}
	versionID, err := s.ids.Next()
	if err != nil {
		return domain.Secret{}, apperr.Internal("generate version id failed", err)
	}
	version := domain.SecretVersion{
		ID: versionID, SecretID: secret.ID, Version: newVersionNo,
		Ciphertext: blob.Ciphertext, Nonce: blob.Nonce, WrappedDEK: blob.WrappedDEK,
		KEKKeyID: blob.KEKKeyID, Algorithm: blob.Algorithm, ValueHash: blob.ValueHash,
		Status: domain.SecretVersionPending, CreatedBy: in.Actor.SubjectID, CreatedAt: now,
	}
	if err := s.secrets.AppendVersion(ctx, version); err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	expected := secret.Version
	secret.PendingAction = domain.PendingActionRotate
	secret.PendingPayload = strconv.FormatInt(newVersionNo, 10)
	secret.SubmittedBy = in.Actor.SubjectID
	secret.UpdatedBy = in.Actor.SubjectID
	secret.UpdatedAt = now
	secret.Version++
	if err := s.secrets.UpdateCAS(ctx, secret, expected); err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	if err := s.requireAudit(ctx, in.Actor, audit.ActionConfigSecretRotate, ResourceTypeSecret,
		strconv.FormatInt(secret.ID, 10), audit.OutcomeSuccess, "submitted:"+in.Comment, map[string]string{
			"name": secret.Name, "pending_version": strconv.FormatInt(newVersionNo, 10),
			"fingerprint": blob.ValueHash,
		}); err != nil {
		return domain.Secret{}, err
	}
	if !in.Actor.IsSystemAdmin() {
		return secret, nil
	}
	return s.ApproveSecret(ctx, ApproveSecretInput{
		ID: secret.ID, Decision: domain.DecisionApproved,
		Comment: "system_admin: skip dual approval", Actor: in.Actor,
	})
}

// RevokeSecret 提交吊销：进入待审，审批通过后再生效。
func (s *SecretService) RevokeSecret(ctx context.Context, secretID int64, comment string, actor Actor) (domain.Secret, error) {
	if err := requireActor(actor); err != nil {
		return domain.Secret{}, err
	}
	secret, err := s.secrets.GetByID(ctx, secretID)
	if err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	if !secret.CanRevoke() {
		return domain.Secret{}, apperr.Conflict("secret cannot be revoked in current status")
	}
	now := s.now().UTC()
	expected := secret.Version
	secret.PendingAction = domain.PendingActionRevoke
	secret.PendingPayload = comment
	secret.SubmittedBy = actor.SubjectID
	secret.UpdatedBy = actor.SubjectID
	secret.UpdatedAt = now
	secret.Version++
	if err := s.secrets.UpdateCAS(ctx, secret, expected); err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	if err := s.requireAudit(ctx, actor, audit.ActionConfigSecretRevoke, ResourceTypeSecret,
		strconv.FormatInt(secret.ID, 10), audit.OutcomeSuccess, "submitted:"+comment, map[string]string{
			"name": secret.Name,
		}); err != nil {
		return domain.Secret{}, err
	}
	if !actor.IsSystemAdmin() {
		return secret, nil
	}
	return s.ApproveSecret(ctx, ApproveSecretInput{
		ID: secret.ID, Decision: domain.DecisionApproved,
		Comment: "system_admin: skip dual approval", Actor: actor,
	})
}

// ActivateVersionInput 是激活历史版本入参（CC-R-14：须破玻璃）。
type ActivateVersionInput struct {
	SecretID         int64
	Version          int64
	BreakGlass       bool
	BreakGlassReason string
	Actor            Actor
}

// ActivateVersion 回滚激活指定版本；须显式 break_glass（绕过轮换审批）。
func (s *SecretService) ActivateVersion(ctx context.Context, in ActivateVersionInput) (domain.Secret, error) {
	if err := requireActor(in.Actor); err != nil {
		return domain.Secret{}, err
	}
	if !in.BreakGlass {
		return domain.Secret{}, apperr.BadRequest(
			"activate version requires break_glass=true (bypasses rotate approval; CC-R-14)")
	}
	if strings.TrimSpace(in.BreakGlassReason) == "" {
		return domain.Secret{}, apperr.BadRequest("break_glass_reason is required to activate a secret version")
	}
	secret, err := s.secrets.GetByID(ctx, in.SecretID)
	if err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	target, err := s.secrets.GetVersion(ctx, in.SecretID, in.Version)
	if err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	if target.Status == domain.SecretVersionDestroyed || target.Status == domain.SecretVersionRevoked {
		return domain.Secret{}, apperr.Conflict("version cannot be activated")
	}
	now := s.now().UTC()
	if secret.ActiveVersion != nil && *secret.ActiveVersion != in.Version {
		if err := s.secrets.UpdateVersionStatus(ctx, in.SecretID, *secret.ActiveVersion,
			domain.SecretVersionActive, domain.SecretVersionGrace, now); err != nil {
			return domain.Secret{}, mapSecretErr(err)
		}
	}
	if target.Status != domain.SecretVersionActive {
		if err := s.secrets.UpdateVersionStatus(ctx, in.SecretID, in.Version, target.Status, domain.SecretVersionActive, now); err != nil {
			return domain.Secret{}, mapSecretErr(err)
		}
	}
	expected := secret.Version
	secret.ActiveVersion = &in.Version
	secret.Fingerprint = target.ValueHash
	secret.Status = domain.SecretStatusActive
	secret.UpdatedBy = in.Actor.SubjectID
	secret.UpdatedAt = now
	secret.Version++
	if err := s.secrets.UpdateCAS(ctx, secret, expected); err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	if err := s.requireAudit(ctx, in.Actor, audit.ActionConfigSecretRotate, ResourceTypeSecret,
		strconv.FormatInt(secret.ID, 10), audit.OutcomeSuccess, "break_glass_activate", map[string]string{
			"name": secret.Name, "version": strconv.FormatInt(in.Version, 10),
			"break_glass": "true", "break_glass_reason": strings.TrimSpace(in.BreakGlassReason),
		}); err != nil {
		return domain.Secret{}, err
	}
	return secret, nil
}

// CreateGrantInput 授权入参。
type CreateGrantInput struct {
	SecretID        int64
	ServiceIdentity string
	ExpiresAt       *time.Time
	Actor           Actor
}

// CreateGrant 提交服务授权（pending）；审批通过后才可解析。
func (s *SecretService) CreateGrant(ctx context.Context, in CreateGrantInput) (domain.SecretGrant, error) {
	if err := requireActor(in.Actor); err != nil {
		return domain.SecretGrant{}, err
	}
	secret, err := s.secrets.GetByID(ctx, in.SecretID)
	if err != nil {
		return domain.SecretGrant{}, mapSecretErr(err)
	}
	if !secret.CanGrant() {
		return domain.SecretGrant{}, apperr.Conflict("secret cannot accept grant in current status")
	}
	grantID, err := s.ids.Next()
	if err != nil {
		return domain.SecretGrant{}, apperr.Internal("generate grant id failed", err)
	}
	now := s.now().UTC()
	grant := domain.SecretGrant{
		ID: grantID, Env: secret.Env, Namespace: secret.Namespace,
		ServiceIdentity: strings.TrimSpace(in.ServiceIdentity), SecretID: secret.ID,
		Permission: "pending", ExpiresAt: in.ExpiresAt,
		CreatedBy: in.Actor.SubjectID, CreatedAt: now,
	}
	if err := grant.Validate(); err != nil {
		return domain.SecretGrant{}, apperr.BadRequest(err.Error())
	}
	if err := s.grants.Create(ctx, grant); err != nil {
		if errors.Is(err, domain.ErrDuplicateKey) {
			return domain.SecretGrant{}, apperr.Conflict("grant already exists")
		}
		return domain.SecretGrant{}, apperr.Internal("create grant failed", err)
	}
	expected := secret.Version
	secret.PendingAction = domain.PendingActionGrant
	secret.PendingPayload = strconv.FormatInt(grantID, 10)
	secret.SubmittedBy = in.Actor.SubjectID
	secret.UpdatedBy = in.Actor.SubjectID
	secret.UpdatedAt = now
	secret.Version++
	if err := s.secrets.UpdateCAS(ctx, secret, expected); err != nil {
		return domain.SecretGrant{}, mapSecretErr(err)
	}
	if err := s.requireAudit(ctx, in.Actor, audit.ActionConfigSecretGrant, ResourceTypeGrant,
		strconv.FormatInt(grantID, 10), audit.OutcomeSuccess, "submitted", map[string]string{
			"secret_id":        strconv.FormatInt(secret.ID, 10),
			"service_identity": grant.ServiceIdentity, "op": "create",
		}); err != nil {
		return domain.SecretGrant{}, err
	}
	if !in.Actor.IsSystemAdmin() {
		return grant, nil
	}
	if _, err := s.ApproveSecret(ctx, ApproveSecretInput{
		ID: secret.ID, Decision: domain.DecisionApproved,
		Comment: "system_admin: skip dual approval", Actor: in.Actor,
	}); err != nil {
		return domain.SecretGrant{}, err
	}
	updated, err := s.grants.GetByID(ctx, grant.ID)
	if err != nil {
		return domain.SecretGrant{}, mapSecretErr(err)
	}
	return updated, nil
}

// RevokeGrant 收回授权。
func (s *SecretService) RevokeGrant(ctx context.Context, secretID, grantID int64, actor Actor) (domain.SecretGrant, error) {
	if err := requireActor(actor); err != nil {
		return domain.SecretGrant{}, err
	}
	grant, err := s.grants.GetByID(ctx, grantID)
	if err != nil {
		return domain.SecretGrant{}, mapSecretErr(err)
	}
	if grant.SecretID != secretID {
		return domain.SecretGrant{}, apperr.NotFound("grant not found")
	}
	now := s.now().UTC()
	if err := s.grants.Revoke(ctx, grantID, now); err != nil {
		return domain.SecretGrant{}, mapSecretErr(err)
	}
	grant.RevokedAt = &now
	if err := s.requireAudit(ctx, actor, audit.ActionConfigSecretGrant, ResourceTypeGrant,
		strconv.FormatInt(grantID, 10), audit.OutcomeSuccess, "", map[string]string{
			"secret_id": strconv.FormatInt(secretID, 10), "op": "revoke",
		}); err != nil {
		return domain.SecretGrant{}, err
	}
	return grant, nil
}

// ResolveInput 内部解析入参。
type ResolveInput struct {
	Env             string
	Namespace       string
	Names           []string
	ServiceIdentity string
	TraceID         string
}

// ResolveResult 单条解析结果。
type ResolveResult struct {
	Name           string
	Version        int64
	Value          string
	GraceVersion   *int64
	GraceValue     string
	LeaseExpiresAt time.Time
}

// Resolve 校验 grant 后解密；单次 ≤ 20 key。
func (s *SecretService) Resolve(ctx context.Context, in ResolveInput) ([]ResolveResult, error) {
	start := s.now()
	observe := func(result string) {
		if s.observer != nil {
			s.observer.ObserveSecretResolve(result, s.now().Sub(start))
		}
	}
	if strings.TrimSpace(in.ServiceIdentity) == "" {
		observe("denied")
		return nil, apperr.Forbidden("service identity required")
	}
	if len(in.Names) == 0 || len(in.Names) > 20 {
		observe("error")
		return nil, apperr.BadRequest("names must contain 1..20 items")
	}
	now := s.now().UTC()
	leaseTTL := s.LeaseTTL
	if leaseTTL <= 0 || leaseTTL > 15*time.Minute {
		leaseTTL = 5 * time.Minute
	}
	leaseExpires := now.Add(leaseTTL)
	out := make([]ResolveResult, 0, len(in.Names))
	for _, name := range in.Names {
		secret, err := s.secrets.GetByName(ctx, in.Env, in.Namespace, name)
		if err != nil {
			s.recordAccess(ctx, in, name, 0, "not_found")
			observe("error")
			return nil, apperr.NotFound(fmt.Sprintf("secret %q not found", name))
		}
		if secret.Status != domain.SecretStatusActive || secret.ActiveVersion == nil {
			s.recordAccess(ctx, in, name, 0, "not_found")
			observe("error")
			return nil, apperr.NotFound(fmt.Sprintf("secret %q is not active", name))
		}
		if _, err := s.grants.FindActive(ctx, in.Env, in.Namespace, in.ServiceIdentity, secret.ID, now); err != nil {
			s.recordAccess(ctx, in, name, *secret.ActiveVersion, "denied")
			if auditErr := s.requireAudit(ctx, Actor{SubjectID: in.ServiceIdentity, ActorType: "service", TraceID: in.TraceID},
				audit.ActionConfigSecretResolve, ResourceTypeSecret, strconv.FormatInt(secret.ID, 10),
				audit.OutcomeDenied, "missing grant", map[string]string{
					"name": name, "service_identity": in.ServiceIdentity,
				}); auditErr != nil {
				observe("error")
				return nil, auditErr
			}
			observe("denied")
			return nil, apperr.Forbidden("secret resolve denied")
		}
		version, err := s.secrets.GetVersion(ctx, secret.ID, *secret.ActiveVersion)
		if err != nil {
			observe("error")
			return nil, apperr.Internal("load secret version failed", err)
		}
		plain, err := envelope.Open(s.kek, envelope.Blob{
			Ciphertext: version.Ciphertext, Nonce: version.Nonce, WrappedDEK: version.WrappedDEK,
			KEKKeyID: version.KEKKeyID, Algorithm: version.Algorithm, ValueHash: version.ValueHash,
		}, envelope.AAD(secret.Env, secret.Namespace, secret.Name, version.Version))
		if err != nil {
			s.recordAccess(ctx, in, name, version.Version, "kms_unavailable")
			observe("unavailable")
			return nil, apperr.Unavailable("secret decryption unavailable")
		}
		item := ResolveResult{
			Name: name, Version: version.Version, Value: string(plain), LeaseExpiresAt: leaseExpires,
		}
		if secret.DualVersion {
			if grace, gerr := s.findGrace(ctx, secret.ID, version.Version); gerr == nil {
				gplain, gerr := envelope.Open(s.kek, envelope.Blob{
					Ciphertext: grace.Ciphertext, Nonce: grace.Nonce, WrappedDEK: grace.WrappedDEK,
					KEKKeyID: grace.KEKKeyID, Algorithm: grace.Algorithm, ValueHash: grace.ValueHash,
				}, envelope.AAD(secret.Env, secret.Namespace, secret.Name, grace.Version))
				if gerr == nil {
					gv := grace.Version
					item.GraceVersion = &gv
					item.GraceValue = string(gplain)
				}
			}
		}
		out = append(out, item)
		s.recordAccess(ctx, in, name, version.Version, "ok")
		if auditErr := s.requireAudit(ctx, Actor{SubjectID: in.ServiceIdentity, ActorType: "service", TraceID: in.TraceID},
			audit.ActionConfigSecretResolve, ResourceTypeSecret, strconv.FormatInt(secret.ID, 10),
			audit.OutcomeSuccess, "", map[string]string{
				"name": name, "version": strconv.FormatInt(version.Version, 10),
				"service_identity": in.ServiceIdentity,
			}); auditErr != nil {
			observe("error")
			return nil, auditErr
		}
	}
	observe("ok")
	return out, nil
}

func (s *SecretService) findGrace(ctx context.Context, secretID, activeVersion int64) (domain.SecretVersion, error) {
	versions, _, err := s.secrets.ListVersions(ctx, secretID, 50, 0)
	if err != nil {
		return domain.SecretVersion{}, err
	}
	for _, meta := range versions {
		if meta.Status == domain.SecretVersionGrace && meta.Version != activeVersion {
			return s.secrets.GetVersion(ctx, secretID, meta.Version)
		}
	}
	return domain.SecretVersion{}, domain.ErrNotFound
}

func (s *SecretService) recordAccess(ctx context.Context, in ResolveInput, name string, version int64, result string) {
	if s.access == nil {
		return
	}
	_ = s.access.Append(ctx, domain.SecretAccess{
		OccurredAt: s.now().UTC(), ServiceIdentity: in.ServiceIdentity,
		SecretName: name, SecretVersion: version, Result: result, TraceID: in.TraceID,
	})
}

// Get 按主键读取密钥元数据。
func (s *SecretService) Get(ctx context.Context, secretID int64) (domain.Secret, error) {
	secret, err := s.secrets.GetByID(ctx, secretID)
	if err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	return secret, nil
}

// List 分页列出密钥元数据。
func (s *SecretService) List(ctx context.Context, env, namespace string, status domain.SecretStatus, limit, offset int) ([]domain.Secret, int, error) {
	return s.secrets.List(ctx, env, namespace, status, limit, offset)
}

// ListVersions 分页列出密文版本元数据。
func (s *SecretService) ListVersions(ctx context.Context, secretID int64, limit, offset int) ([]domain.SecretVersion, int, error) {
	if _, err := s.secrets.GetByID(ctx, secretID); err != nil {
		return nil, 0, mapSecretErr(err)
	}
	return s.secrets.ListVersions(ctx, secretID, limit, offset)
}

// ListGrants 列出密钥授权。
func (s *SecretService) ListGrants(ctx context.Context, secretID int64) ([]domain.SecretGrant, error) {
	if _, err := s.secrets.GetByID(ctx, secretID); err != nil {
		return nil, mapSecretErr(err)
	}
	return s.grants.ListBySecret(ctx, secretID)
}

// ListAccesses 分页列出密钥访问审计。
func (s *SecretService) ListAccesses(ctx context.Context, secretID int64, limit, offset int) ([]domain.SecretAccess, int, error) {
	secret, err := s.secrets.GetByID(ctx, secretID)
	if err != nil {
		return nil, 0, mapSecretErr(err)
	}
	if s.access == nil {
		return nil, 0, nil
	}
	return s.access.ListBySecretName(ctx, secret.Env, secret.Namespace, secret.Name, limit, offset)
}

func mapSecretErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return apperr.NotFound("secret not found")
	case errors.Is(err, domain.ErrDuplicateKey):
		return apperr.Conflict("secret conflict")
	case errors.Is(err, domain.ErrVersionConflict), errors.Is(err, domain.ErrStateConflict):
		return apperr.Conflict("secret version or state conflict")
	default:
		return apperr.Internal("secret operation failed", err)
	}
}
