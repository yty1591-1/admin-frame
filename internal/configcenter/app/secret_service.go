package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/envelope"
	"github.com/admin-frame/admin-frame/pkg/id"
)

const (
	ResourceTypeSecret = "config_secret"
	ResourceTypeGrant  = "config_secret_grant"
)

// SecretService 是业务密钥管理与解析用例。
type SecretService struct {
	secrets domain.SecretRepository
	grants  domain.SecretGrantRepository
	access  domain.SecretAccessRepository
	kek     envelope.KEKProvider
	audit   auditContext
	ids     id.Generator
	now     func() time.Time
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
		audit:    auditContext{emitter: emitter, ids: ids, service: opts.Service, now: now},
		ids:      ids,
		now:      now,
		LeaseTTL: 5 * time.Minute,
	}, nil
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
	_ = s.audit.emit(ctx, in.Actor, audit.ActionConfigSecretCreate, ResourceTypeSecret,
		strconv.FormatInt(secretID, 10), audit.OutcomeSuccess, "", map[string]string{
			"env": in.Env, "namespace": in.Namespace, "name": in.Name,
			"version": "1", "fingerprint": blob.ValueHash,
		})
	return secret, nil
}

// SubmitSecret 送审：draft/rejected → pending_review。
func (s *SecretService) SubmitSecret(ctx context.Context, id int64, actor Actor) (domain.Secret, error) {
	if err := requireActor(actor); err != nil {
		return domain.Secret{}, err
	}
	secret, err := s.secrets.GetByID(ctx, id)
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
	return secret, nil
}

// ApproveSecretInput 审批入参。
type ApproveSecretInput struct {
	ID       int64
	Decision domain.ReviewDecision
	Comment  string
	Actor    Actor
}

// ApproveSecret 双人审批；提交人不得自批。
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
	if secret.SubmittedBy == in.Actor.SubjectID {
		return domain.Secret{}, apperr.Forbidden("submitter cannot review their own secret change")
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
	_ = s.audit.emit(ctx, in.Actor, audit.ActionConfigSecretCreate, ResourceTypeSecret,
		strconv.FormatInt(secret.ID, 10), audit.OutcomeSuccess, string(in.Decision), map[string]string{
			"name": secret.Name, "decision": string(in.Decision),
		})
	return secret, nil
}

// RotateSecretInput 轮换入参。
type RotateSecretInput struct {
	ID        int64
	Plaintext string
	Comment   string
	Actor     Actor
}

// RotateSecret 写入新版本并令旧版本进入 grace。
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
		Status: domain.SecretVersionActive, CreatedBy: in.Actor.SubjectID, CreatedAt: now,
	}
	if err := s.secrets.AppendVersion(ctx, version); err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	if oldVersion > 0 {
		_ = s.secrets.UpdateVersionStatus(ctx, secret.ID, oldVersion,
			domain.SecretVersionActive, domain.SecretVersionGrace, now)
	}
	expected := secret.Version
	secret.ActiveVersion = &newVersionNo
	secret.Fingerprint = blob.ValueHash
	secret.UpdatedBy = in.Actor.SubjectID
	secret.UpdatedAt = now
	secret.Version++
	if err := s.secrets.UpdateCAS(ctx, secret, expected); err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	_ = s.audit.emit(ctx, in.Actor, audit.ActionConfigSecretRotate, ResourceTypeSecret,
		strconv.FormatInt(secret.ID, 10), audit.OutcomeSuccess, in.Comment, map[string]string{
			"name": secret.Name, "old_version": strconv.FormatInt(oldVersion, 10),
			"new_version": strconv.FormatInt(newVersionNo, 10), "fingerprint": blob.ValueHash,
		})
	return secret, nil
}

// RevokeSecret 吊销密钥。
func (s *SecretService) RevokeSecret(ctx context.Context, id int64, comment string, actor Actor) (domain.Secret, error) {
	if err := requireActor(actor); err != nil {
		return domain.Secret{}, err
	}
	secret, err := s.secrets.GetByID(ctx, id)
	if err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	if !secret.CanRevoke() {
		return domain.Secret{}, apperr.Conflict("secret cannot be revoked in current status")
	}
	now := s.now().UTC()
	if secret.ActiveVersion != nil {
		_ = s.secrets.UpdateVersionStatus(ctx, secret.ID, *secret.ActiveVersion,
			domain.SecretVersionActive, domain.SecretVersionRevoked, now)
	}
	expected := secret.Version
	secret.Status = domain.SecretStatusRevoked
	secret.UpdatedBy = actor.SubjectID
	secret.UpdatedAt = now
	secret.Version++
	if err := s.secrets.UpdateCAS(ctx, secret, expected); err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	_ = s.audit.emit(ctx, actor, audit.ActionConfigSecretRevoke, ResourceTypeSecret,
		strconv.FormatInt(secret.ID, 10), audit.OutcomeSuccess, comment, map[string]string{
			"name": secret.Name,
		})
	return secret, nil
}

// ActivateVersion 回滚激活指定版本。
func (s *SecretService) ActivateVersion(ctx context.Context, id, version int64, actor Actor) (domain.Secret, error) {
	if err := requireActor(actor); err != nil {
		return domain.Secret{}, err
	}
	secret, err := s.secrets.GetByID(ctx, id)
	if err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	target, err := s.secrets.GetVersion(ctx, id, version)
	if err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	if target.Status == domain.SecretVersionDestroyed || target.Status == domain.SecretVersionRevoked {
		return domain.Secret{}, apperr.Conflict("version cannot be activated")
	}
	now := s.now().UTC()
	if secret.ActiveVersion != nil && *secret.ActiveVersion != version {
		_ = s.secrets.UpdateVersionStatus(ctx, id, *secret.ActiveVersion,
			domain.SecretVersionActive, domain.SecretVersionGrace, now)
	}
	if target.Status != domain.SecretVersionActive {
		_ = s.secrets.UpdateVersionStatus(ctx, id, version, target.Status, domain.SecretVersionActive, now)
	}
	expected := secret.Version
	secret.ActiveVersion = &version
	secret.Fingerprint = target.ValueHash
	secret.Status = domain.SecretStatusActive
	secret.UpdatedBy = actor.SubjectID
	secret.UpdatedAt = now
	secret.Version++
	if err := s.secrets.UpdateCAS(ctx, secret, expected); err != nil {
		return domain.Secret{}, mapSecretErr(err)
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

// CreateGrant 新增服务授权。
func (s *SecretService) CreateGrant(ctx context.Context, in CreateGrantInput) (domain.SecretGrant, error) {
	if err := requireActor(in.Actor); err != nil {
		return domain.SecretGrant{}, err
	}
	secret, err := s.secrets.GetByID(ctx, in.SecretID)
	if err != nil {
		return domain.SecretGrant{}, mapSecretErr(err)
	}
	grantID, err := s.ids.Next()
	if err != nil {
		return domain.SecretGrant{}, apperr.Internal("generate grant id failed", err)
	}
	now := s.now().UTC()
	grant := domain.SecretGrant{
		ID: grantID, Env: secret.Env, Namespace: secret.Namespace,
		ServiceIdentity: strings.TrimSpace(in.ServiceIdentity), SecretID: secret.ID,
		Permission: "resolve", ExpiresAt: in.ExpiresAt,
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
	_ = s.audit.emit(ctx, in.Actor, audit.ActionConfigSecretGrant, ResourceTypeGrant,
		strconv.FormatInt(grantID, 10), audit.OutcomeSuccess, "", map[string]string{
			"secret_id": strconv.FormatInt(secret.ID, 10),
			"service_identity": grant.ServiceIdentity, "op": "create",
		})
	return grant, nil
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
	_ = s.audit.emit(ctx, actor, audit.ActionConfigSecretGrant, ResourceTypeGrant,
		strconv.FormatInt(grantID, 10), audit.OutcomeSuccess, "", map[string]string{
			"secret_id": strconv.FormatInt(secretID, 10), "op": "revoke",
		})
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
	if strings.TrimSpace(in.ServiceIdentity) == "" {
		return nil, apperr.Forbidden("service identity required")
	}
	if len(in.Names) == 0 || len(in.Names) > 20 {
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
			return nil, apperr.NotFound(fmt.Sprintf("secret %q not found", name))
		}
		if secret.Status != domain.SecretStatusActive || secret.ActiveVersion == nil {
			s.recordAccess(ctx, in, name, 0, "not_found")
			return nil, apperr.NotFound(fmt.Sprintf("secret %q is not active", name))
		}
		if _, err := s.grants.FindActive(ctx, in.Env, in.Namespace, in.ServiceIdentity, secret.ID, now); err != nil {
			s.recordAccess(ctx, in, name, *secret.ActiveVersion, "denied")
			_ = s.audit.emit(ctx, Actor{SubjectID: in.ServiceIdentity, ActorType: "service", TraceID: in.TraceID},
				audit.ActionConfigSecretResolve, ResourceTypeSecret, strconv.FormatInt(secret.ID, 10),
				audit.OutcomeDenied, "missing grant", map[string]string{
					"name": name, "service_identity": in.ServiceIdentity,
				})
			return nil, apperr.Forbidden("secret resolve denied")
		}
		version, err := s.secrets.GetVersion(ctx, secret.ID, *secret.ActiveVersion)
		if err != nil {
			return nil, apperr.Internal("load secret version failed", err)
		}
		plain, err := envelope.Open(s.kek, envelope.Blob{
			Ciphertext: version.Ciphertext, Nonce: version.Nonce, WrappedDEK: version.WrappedDEK,
			KEKKeyID: version.KEKKeyID, Algorithm: version.Algorithm, ValueHash: version.ValueHash,
		}, envelope.AAD(secret.Env, secret.Namespace, secret.Name, version.Version))
		if err != nil {
			s.recordAccess(ctx, in, name, version.Version, "kms_unavailable")
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
		_ = s.audit.emit(ctx, Actor{SubjectID: in.ServiceIdentity, ActorType: "service", TraceID: in.TraceID},
			audit.ActionConfigSecretResolve, ResourceTypeSecret, strconv.FormatInt(secret.ID, 10),
			audit.OutcomeSuccess, "", map[string]string{
				"name": name, "version": strconv.FormatInt(version.Version, 10),
				"service_identity": in.ServiceIdentity,
			})
	}
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

// Get / List / ListVersions / ListGrants / ListAccesses 只读查询。

func (s *SecretService) Get(ctx context.Context, id int64) (domain.Secret, error) {
	secret, err := s.secrets.GetByID(ctx, id)
	if err != nil {
		return domain.Secret{}, mapSecretErr(err)
	}
	return secret, nil
}

func (s *SecretService) List(ctx context.Context, env, namespace string, status domain.SecretStatus, limit, offset int) ([]domain.Secret, int, error) {
	return s.secrets.List(ctx, env, namespace, status, limit, offset)
}

func (s *SecretService) ListVersions(ctx context.Context, id int64, limit, offset int) ([]domain.SecretVersion, int, error) {
	if _, err := s.secrets.GetByID(ctx, id); err != nil {
		return nil, 0, mapSecretErr(err)
	}
	return s.secrets.ListVersions(ctx, id, limit, offset)
}

func (s *SecretService) ListGrants(ctx context.Context, id int64) ([]domain.SecretGrant, error) {
	if _, err := s.secrets.GetByID(ctx, id); err != nil {
		return nil, mapSecretErr(err)
	}
	return s.grants.ListBySecret(ctx, id)
}

func (s *SecretService) ListAccesses(ctx context.Context, id int64, limit, offset int) ([]domain.SecretAccess, int, error) {
	secret, err := s.secrets.GetByID(ctx, id)
	if err != nil {
		return nil, 0, mapSecretErr(err)
	}
	if s.access == nil {
		return nil, 0, nil
	}
	return s.access.ListBySecretName(ctx, secret.Env, secret.Namespace, secret.Name, limit, offset)
}

func mapSecretErr(err error) error {
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
