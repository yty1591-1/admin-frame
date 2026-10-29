package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// repoBaseTime 是仓储用例的固定时间基准。
var repoBaseTime = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// mustAccount 构造合法账号。
func mustAccount(t *testing.T, id string, channel domain.Channel, subject string) *domain.Account {
	t.Helper()
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID:      id,
		Channel: channel,
		Subject: subject,
		Now:     repoBaseTime,
	})
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	return account
}

// mustSession 构造合法会话。
func mustSession(t *testing.T, id, family, refreshHash string, createdAt time.Time) *domain.Session {
	t.Helper()
	session, err := domain.NewSession(domain.NewSessionParams{
		ID:           id,
		AccountID:    "1001",
		FamilyID:     family,
		RefreshHash:  refreshHash,
		TokenVersion: 1,
		ExpiresAt:    createdAt.Add(24 * time.Hour),
		Now:          createdAt,
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	return session
}

// mustAttempt 构造登录尝试。
func mustAttempt(t *testing.T, id string, channel domain.Channel, ref string, outcome domain.LoginOutcome, at time.Time) *domain.LoginAttempt {
	t.Helper()
	attempt, err := domain.NewLoginAttempt(domain.NewLoginAttemptParams{
		ID:         id,
		Channel:    channel,
		AccountRef: ref,
		Outcome:    outcome,
		OccurredAt: at,
	})
	if err != nil {
		t.Fatalf("NewLoginAttempt() error = %v", err)
	}
	return attempt
}

// codeOf 返回错误的业务码；非 AppError 返回 0。
func codeOf(err error) int {
	var applicationError *apperr.AppError
	if errors.As(err, &applicationError) {
		return applicationError.Code
	}
	return 0
}

func TestMemoryAccountRepositorySave(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryAccountRepository()
	account := mustAccount(t, "1001", domain.ChannelAdmin, "  Alice ")

	if err := repo.Save(ctx, account, 0); err != nil {
		t.Fatalf("Save(create) error = %v", err)
	}
	if err := repo.Save(ctx, account, 0); codeOf(err) != apperr.CodeConflict {
		t.Fatalf("Save(duplicate id) error = %v, want conflict", err)
	}
	if err := repo.Save(ctx, mustAccount(t, "1002", domain.ChannelAdmin, "alice"), 0); codeOf(err) != apperr.CodeConflict {
		t.Fatalf("Save(duplicate subject) error = %v, want conflict", err)
	}

	found, err := repo.FindByID(ctx, account.ID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	bySubject, err := repo.FindBySubject(ctx, domain.ChannelAdmin, " ALICE ")
	if err != nil {
		t.Fatalf("FindBySubject() error = %v", err)
	}
	if bySubject.ID != found.ID {
		t.Fatalf("FindBySubject() id = %s, want %s", bySubject.ID, found.ID)
	}
	if _, err := repo.FindByID(ctx, "missing"); codeOf(err) != apperr.CodeNotFound {
		t.Fatalf("FindByID(missing) error = %v, want not found", err)
	}
}

func TestMemoryAccountRepositoryOptimisticLock(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryAccountRepository()
	account := mustAccount(t, "1001", domain.ChannelAdmin, "alice")
	if err := repo.Save(ctx, account, 0); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := account.Activate(repoBaseTime); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}

	if err := repo.Save(ctx, account, 0); codeOf(err) != apperr.CodeConflict {
		t.Fatalf("Save(stale create) error = %v, want conflict", err)
	}
	if err := repo.Save(ctx, account, 99); codeOf(err) != apperr.CodeConflict {
		t.Fatalf("Save(wrong version) error = %v, want conflict", err)
	}
	if err := repo.Save(ctx, account, 1); err != nil {
		t.Fatalf("Save(correct version) error = %v", err)
	}

	missing, err := domain.NewAccount(domain.NewAccountParams{
		ID:      "2002",
		Channel: domain.ChannelAdmin,
		Subject: "bob",
		Now:     repoBaseTime,
	})
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	if err := repo.Save(ctx, missing, 1); codeOf(err) != apperr.CodeNotFound {
		t.Fatalf("Save(unknown account) error = %v, want not found", err)
	}
}

func TestMemoryAccountRepositoryImmutability(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryAccountRepository()
	account := mustAccount(t, "1001", domain.ChannelAdmin, "alice")
	if err := repo.Save(ctx, account, 0); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	account.Subject = "bob"
	if err := repo.Save(ctx, account, 1); codeOf(err) != apperr.CodeBadRequest {
		t.Fatalf("Save(changed subject) error = %v, want bad request", err)
	}
}

func TestMemoryAccountRepositoryDeepCopy(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryAccountRepository()
	account := mustAccount(t, "1001", domain.ChannelAdmin, "alice")
	if err := repo.Save(ctx, account, 0); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := repo.FindByID(ctx, account.ID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	loaded.Status = domain.AccountStatusDisabled
	loaded.TokenVersion = 99
	again, err := repo.FindByID(ctx, account.ID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if again.Status != domain.AccountStatusPending || again.TokenVersion != 1 {
		t.Fatalf("stored account mutated: status %s, token version %d", again.Status, again.TokenVersion)
	}
}

func TestMemoryAccountRepositoryDeletedSubjectStillReserved(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryAccountRepository()
	account := mustAccount(t, "1001", domain.ChannelAdmin, "alice")
	if err := repo.Save(ctx, account, 0); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := account.Delete(repoBaseTime); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := repo.Save(ctx, account, 1); err != nil {
		t.Fatalf("Save(delete) error = %v", err)
	}

	found, err := repo.FindBySubject(ctx, domain.ChannelAdmin, "alice")
	if err != nil {
		t.Fatalf("FindBySubject() error = %v", err)
	}
	if !found.IsDeleted() {
		t.Fatal("soft deleted account must still be found by subject")
	}
	if err := repo.Save(ctx, mustAccount(t, "1002", domain.ChannelAdmin, "alice"), 0); codeOf(err) != apperr.CodeConflict {
		t.Fatalf("Save(reuse subject) error = %v, want conflict", err)
	}
}

func TestMemoryCredentialRepository(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryCredentialRepository()
	credential, err := domain.NewCredential(domain.NewCredentialParams{
		ID:        "3001",
		AccountID: "1001",
		Type:      domain.CredentialTypePassword,
		Hash:      "hash-1",
		Algorithm: "argon2id",
		Now:       repoBaseTime,
	})
	if err != nil {
		t.Fatalf("NewCredential() error = %v", err)
	}
	if err := repo.Save(ctx, credential); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	phone, err := domain.NewCredential(domain.NewCredentialParams{
		ID:        "3002",
		AccountID: "1001",
		Type:      domain.CredentialTypePhone,
		Hash:      "phone-hash",
		Algorithm: "hmac-sha256",
		Now:       repoBaseTime.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("NewCredential() error = %v", err)
	}
	if err := repo.Save(ctx, phone); err != nil {
		t.Fatalf("Save(phone) error = %v", err)
	}

	list, err := repo.ListByAccount(ctx, "1001")
	if err != nil {
		t.Fatalf("ListByAccount() error = %v", err)
	}
	if len(list) != 2 || list[0].Type != domain.CredentialTypePassword || list[1].Type != domain.CredentialTypePhone {
		t.Fatalf("ListByAccount() = %+v, want password then phone", list)
	}

	// 同账号同类型替换：保留原主键与创建时间。
	replacement, err := domain.NewCredential(domain.NewCredentialParams{
		ID:        "9999",
		AccountID: "1001",
		Type:      domain.CredentialTypePassword,
		Hash:      "hash-2",
		Algorithm: "bcrypt",
		Now:       repoBaseTime.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("NewCredential() error = %v", err)
	}
	if err := repo.Save(ctx, replacement); err != nil {
		t.Fatalf("Save(replacement) error = %v", err)
	}
	stored, err := repo.FindByAccount(ctx, "1001", domain.CredentialTypePassword)
	if err != nil {
		t.Fatalf("FindByAccount() error = %v", err)
	}
	if stored.Hash != "hash-2" || stored.ID != "3001" || !stored.CreatedAt.Equal(repoBaseTime) {
		t.Fatalf("stored credential = %+v, want replacement keeping id and created_at", stored)
	}

	// 主键冲突：新类型复用已占用 ID 必须拒绝。
	conflicting, err := domain.NewCredential(domain.NewCredentialParams{
		ID:        "3002",
		AccountID: "1002",
		Type:      domain.CredentialTypePassword,
		Hash:      "hash-3",
		Algorithm: "argon2id",
		Now:       repoBaseTime,
	})
	if err != nil {
		t.Fatalf("NewCredential() error = %v", err)
	}
	if err := repo.Save(ctx, conflicting); codeOf(err) != apperr.CodeConflict {
		t.Fatalf("Save(duplicate id) error = %v, want conflict", err)
	}

	if _, err := repo.FindByAccount(ctx, "1001", domain.CredentialType("email")); codeOf(err) != apperr.CodeNotFound {
		t.Fatalf("FindByAccount(missing) error = %v, want not found", err)
	}
	if err := repo.Delete(ctx, "1001", domain.CredentialTypePassword); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := repo.Delete(ctx, "1001", domain.CredentialTypePassword); err != nil {
		t.Fatalf("Delete(idempotent) error = %v", err)
	}
	if _, err := repo.FindByAccount(ctx, "1001", domain.CredentialTypePassword); codeOf(err) != apperr.CodeNotFound {
		t.Fatalf("FindByAccount(deleted) error = %v, want not found", err)
	}
}

func TestMemorySessionRepository(t *testing.T) {
	ctx := context.Background()
	repo := NewMemorySessionRepository()
	session := mustSession(t, "2001", "2001", "hash-1", repoBaseTime)
	if err := repo.Save(ctx, session, 0); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := repo.FindByID(ctx, "2001"); err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if _, err := repo.FindByRefreshHash(ctx, "hash-1"); err != nil {
		t.Fatalf("FindByRefreshHash() error = %v", err)
	}

	if err := repo.Save(ctx, mustSession(t, "2002", "2002", "hash-1", repoBaseTime), 0); codeOf(err) != apperr.CodeConflict {
		t.Fatalf("Save(duplicate hash) error = %v, want conflict", err)
	}
	if err := repo.Save(ctx, session, 0); codeOf(err) != apperr.CodeConflict {
		t.Fatalf("Save(duplicate id) error = %v, want conflict", err)
	}
	if err := session.Rotate(repoBaseTime.Add(time.Minute)); err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	if err := repo.Save(ctx, session, 1); err != nil {
		t.Fatalf("Save(correct version) error = %v", err)
	}
	if err := repo.Save(ctx, session, 5); codeOf(err) != apperr.CodeConflict {
		t.Fatalf("Save(wrong version) error = %v, want conflict", err)
	}
	loaded, err := repo.FindByID(ctx, "2001")
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if loaded.Status != domain.SessionStatusRotated {
		t.Fatalf("status = %s, want rotated", loaded.Status)
	}
	if _, err := repo.FindByRefreshHash(ctx, "missing"); codeOf(err) != apperr.CodeNotFound {
		t.Fatalf("FindByRefreshHash(missing) error = %v, want not found", err)
	}
}

func TestMemorySessionRepositoryRevokeFamily(t *testing.T) {
	ctx := context.Background()
	repo := NewMemorySessionRepository()
	base := repoBaseTime

	active := mustSession(t, "3001", "family-1", "hash-1", base)
	rotated := mustSession(t, "3002", "family-1", "hash-2", base.Add(time.Minute))
	if err := rotated.Rotate(base.Add(2 * time.Minute)); err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	revoked := mustSession(t, "3003", "family-1", "hash-3", base.Add(2*time.Minute))
	if err := revoked.Revoke(domain.RevokeReasonLogout, base.Add(3*time.Minute)); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	otherFamily := mustSession(t, "3004", "family-2", "hash-4", base.Add(3*time.Minute))

	for _, session := range []*domain.Session{active, rotated, revoked, otherFamily} {
		if err := repo.Save(ctx, session, 0); err != nil {
			t.Fatalf("Save(%s) error = %v", session.ID, err)
		}
	}

	count, err := repo.RevokeFamily(ctx, "family-1", domain.RevokeReasonReplay, base.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("RevokeFamily() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("RevokeFamily() = %d, want 2", count)
	}

	for _, id := range []string{"3001", "3002", "3003"} {
		session, err := repo.FindByID(ctx, id)
		if err != nil {
			t.Fatalf("FindByID(%s) error = %v", id, err)
		}
		if session.Status != domain.SessionStatusRevoked {
			t.Fatalf("session %s status = %s, want revoked", id, session.Status)
		}
	}
	untouched, err := repo.FindByID(ctx, "3004")
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if untouched.Status != domain.SessionStatusActive {
		t.Fatalf("other family status = %s, want active", untouched.Status)
	}

	if _, err := repo.RevokeFamily(ctx, "", domain.RevokeReasonReplay, base); codeOf(err) != apperr.CodeBadRequest {
		t.Fatalf("RevokeFamily(empty) error = %v, want bad request", err)
	}
}

func TestMemorySessionRepositoryListByAccount(t *testing.T) {
	ctx := context.Background()
	repo := NewMemorySessionRepository()
	oldest := mustSession(t, "2001", "2001", "hash-1", repoBaseTime)
	newest := mustSession(t, "2003", "2003", "hash-3", repoBaseTime.Add(2*time.Hour))
	middle := mustSession(t, "2002", "2002", "hash-2", repoBaseTime.Add(time.Hour))
	for _, session := range []*domain.Session{oldest, newest, middle} {
		if err := repo.Save(ctx, session, 0); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}
	other, err := domain.NewSession(domain.NewSessionParams{
		ID:           "2004",
		AccountID:    "1002",
		FamilyID:     "2004",
		RefreshHash:  "hash-4",
		TokenVersion: 1,
		ExpiresAt:    repoBaseTime.Add(24 * time.Hour),
		Now:          repoBaseTime,
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if err := repo.Save(ctx, other, 0); err != nil {
		t.Fatalf("Save(other) error = %v", err)
	}

	list, err := repo.ListByAccount(ctx, "1001")
	if err != nil {
		t.Fatalf("ListByAccount() error = %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("ListByAccount() = %d sessions, want 3", len(list))
	}
	for i, want := range []string{"2003", "2002", "2001"} {
		if list[i].ID != want {
			t.Fatalf("list[%d] = %s, want %s", i, list[i].ID, want)
		}
	}
}

func TestMemoryMFAEnrollmentRepository(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryMFAEnrollmentRepository()
	enrollment, err := domain.NewMFAEnrollment("1001", []byte("ciphertext"), repoBaseTime)
	if err != nil {
		t.Fatalf("NewMFAEnrollment() error = %v", err)
	}
	if err := enrollment.SetRecoveryCodes([]string{"hash-1"}, repoBaseTime); err != nil {
		t.Fatalf("SetRecoveryCodes() error = %v", err)
	}
	if err := repo.Save(ctx, enrollment, 0); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := repo.FindByAccount(ctx, "1001")
	if err != nil {
		t.Fatalf("FindByAccount() error = %v", err)
	}
	loaded.SecretCiphertext[0] = 'X'
	loaded.RecoveryCodeHashes[0] = "tampered"
	again, err := repo.FindByAccount(ctx, "1001")
	if err != nil {
		t.Fatalf("FindByAccount() error = %v", err)
	}
	if string(again.SecretCiphertext) != "ciphertext" || again.RecoveryCodeHashes[0] != "hash-1" {
		t.Fatalf("stored enrollment mutated: %q / %v", again.SecretCiphertext, again.RecoveryCodeHashes)
	}

	if err := repo.Delete(ctx, "1001"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := repo.Delete(ctx, "1001"); err != nil {
		t.Fatalf("Delete(idempotent) error = %v", err)
	}
	if _, err := repo.FindByAccount(ctx, "1001"); codeOf(err) != apperr.CodeNotFound {
		t.Fatalf("FindByAccount(deleted) error = %v, want not found", err)
	}
}

func TestMemoryLoginAttemptRepository(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryLoginAttemptRepository()
	ref := "account-ref"

	attempts := []*domain.LoginAttempt{
		mustAttempt(t, "1", domain.ChannelAdmin, ref, domain.LoginOutcomeFailure, repoBaseTime),
		mustAttempt(t, "2", domain.ChannelAdmin, ref, domain.LoginOutcomeFailure, repoBaseTime.Add(time.Minute)),
		mustAttempt(t, "3", domain.ChannelAdmin, ref, domain.LoginOutcomeSuccess, repoBaseTime.Add(2*time.Minute)),
		mustAttempt(t, "4", domain.ChannelAdmin, ref, domain.LoginOutcomeFailure, repoBaseTime.Add(3*time.Minute)),
		mustAttempt(t, "5", domain.ChannelBiz, ref, domain.LoginOutcomeFailure, repoBaseTime.Add(4*time.Minute)),
	}
	for _, attempt := range attempts {
		if err := repo.Append(ctx, attempt); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	tests := []struct {
		name  string
		since time.Time
		want  int
	}{
		{name: "all failures", since: repoBaseTime, want: 3},
		{name: "since boundary inclusive", since: repoBaseTime.Add(time.Minute), want: 2},
		{name: "after all failures", since: repoBaseTime.Add(4 * time.Minute), want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.CountFailuresSince(ctx, domain.ChannelAdmin, ref, tt.since)
			if err != nil {
				t.Fatalf("CountFailuresSince() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("CountFailuresSince() = %d, want %d", got, tt.want)
			}
		})
	}

	latest, found, err := repo.LastSuccessAt(ctx, domain.ChannelAdmin, ref)
	if err != nil {
		t.Fatalf("LastSuccessAt() error = %v", err)
	}
	if !found || !latest.Equal(repoBaseTime.Add(2*time.Minute)) {
		t.Fatalf("LastSuccessAt() = %v / %v, want %v / true", latest, found, repoBaseTime.Add(2*time.Minute))
	}
	if _, found, err := repo.LastSuccessAt(ctx, domain.Channel("partner"), ref); err != nil || found {
		t.Fatalf("LastSuccessAt(unknown channel) = found %v, err %v; want false / nil", found, err)
	}
	if err := repo.Append(ctx, nil); codeOf(err) != apperr.CodeBadRequest {
		t.Fatalf("Append(nil) error = %v, want bad request", err)
	}
}
