package identity_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/pkg/crypto"
	"github.com/admin-frame/admin-frame/pkg/testkit"
)

// newIntegrationProtector 构造固定密钥的主体保护器。
func newIntegrationProtector(t *testing.T) persistence.SubjectProtector {
	t.Helper()
	hasher, err := crypto.NewHasher("v1", map[string][]byte{"v1": bytes.Repeat([]byte{0x11}, 32)})
	if err != nil {
		t.Fatalf("new hasher: %v", err)
	}
	ring, err := crypto.NewKeyRing(map[string][]byte{"k1": bytes.Repeat([]byte{0x22}, 32)}, "k1", nil)
	if err != nil {
		t.Fatalf("new key ring: %v", err)
	}
	t.Cleanup(ring.Close)
	return persistence.NewSubjectProtector(hasher, ring)
}

// TestSQLAccountSubjectProtection 验证 S1/S2 双写双读与 S3 回填。
func TestSQLAccountSubjectProtection(t *testing.T) {
	ctx := context.Background()
	sqlDB := testkit.MySQL(t).SQL()
	protector := newIntegrationProtector(t)
	repo := persistence.NewSQLAccountRepository(sqlDB, persistence.WithSubjectProtector(protector))
	plainRepo := persistence.NewSQLAccountRepository(sqlDB)

	now := time.Now().UTC()
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID:      "900000000000000101",
		Channel: domain.ChannelAdmin,
		Subject: "SQL-Subject@Example.com",
		Now:     now,
	})
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	if err := repo.Save(ctx, account, 0); err != nil {
		t.Fatalf("save protected account: %v", err)
	}

	// 双写：库内 index / ciphertext 非空，且密文不含明文。
	var index []byte
	var ciphertext []byte
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT subject_index, subject_ciphertext FROM identity_account WHERE id = ?`,
		account.ID).Scan(&index, &ciphertext); err != nil {
		t.Fatalf("read protection columns: %v", err)
	}
	if len(index) == 0 || len(ciphertext) == 0 {
		t.Fatalf("protection columns must be non-empty, index=%q ciphertext=%d", index, len(ciphertext))
	}
	if bytes.Contains(ciphertext, []byte("SQL-Subject@Example.com")) {
		t.Fatalf("ciphertext must not contain plaintext subject")
	}

	// 双读：大小写 / 空白归一化后按 index 命中，并解密回填明文。
	found, err := repo.FindBySubject(ctx, domain.ChannelAdmin, "", " sql-subject@example.com ")
	if err != nil {
		t.Fatalf("find by subject via index: %v", err)
	}
	if found.Subject != "sql-subject@example.com" {
		t.Fatalf("decoded subject = %q", found.Subject)
	}

	// 换绑：同步更新 index / ciphertext，旧主体不再命中。
	if err := found.RebindSubject("sql-new@example.com", now.Add(time.Minute)); err != nil {
		t.Fatalf("rebind subject: %v", err)
	}
	if err := repo.RebindSubject(ctx, found, found.Version-1); err != nil {
		t.Fatalf("persist rebind: %v", err)
	}
	if _, err := repo.FindBySubject(ctx, domain.ChannelAdmin, "", "sql-subject@example.com"); err == nil {
		t.Fatalf("old subject must not resolve after rebind")
	}
	if _, err := repo.FindBySubject(ctx, domain.ChannelAdmin, "", "sql-new@example.com"); err != nil {
		t.Fatalf("new subject must resolve: %v", err)
	}

	// S3 回填：创建一条未双写的存量行，回填后可按 index 查找。
	legacy, err := domain.NewAccount(domain.NewAccountParams{
		ID:      "900000000000000102",
		Channel: domain.ChannelAdmin,
		Subject: "sql-legacy@example.com",
		Now:     now,
	})
	if err != nil {
		t.Fatalf("new legacy account: %v", err)
	}
	if err := plainRepo.Save(ctx, legacy, 0); err != nil {
		t.Fatalf("save legacy account: %v", err)
	}
	updated, err := repo.BackfillSubjectProtection(ctx, 100)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if updated == 0 {
		t.Fatalf("backfill should update at least the legacy row")
	}
	if _, err := repo.FindBySubject(ctx, domain.ChannelAdmin, "", "sql-legacy@example.com"); err != nil {
		t.Fatalf("legacy subject must resolve after backfill: %v", err)
	}
}

// TestSQLSessionArchiveSameTransaction 验证归档写入与在线删除在同一事务内生效。
func TestSQLSessionArchiveSameTransaction(t *testing.T) {
	ctx := context.Background()
	sqlDB := testkit.MySQL(t).SQL()
	sessions := persistence.NewSQLSessionRepository(sqlDB)
	accounts := persistence.NewSQLAccountRepository(sqlDB)

	now := time.Now().UTC()
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID:      "900000000000000201",
		Channel: domain.ChannelAdmin,
		Subject: "sql-archive@example.com",
		Now:     now,
	})
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	if err := accounts.Save(ctx, account, 0); err != nil {
		t.Fatalf("save account: %v", err)
	}

	family, err := domain.NewSessionFamily(domain.NewSessionFamilyParams{
		ID:                "900000000000000202",
		AccountID:         account.ID,
		Now:               now,
		ExpiresAt:         now.Add(48 * time.Hour),
		AbsoluteExpiresAt: now.Add(72 * time.Hour),
	})
	if err != nil {
		t.Fatalf("new family: %v", err)
	}
	if err := family.Revoke(domain.RevokeReasonLogout, now); err != nil {
		t.Fatalf("revoke family: %v", err)
	}
	if err := sessions.SaveFamily(ctx, family, 0); err != nil {
		t.Fatalf("save family: %v", err)
	}

	session, err := domain.NewSession(domain.NewSessionParams{
		ID:           "900000000000000203",
		AccountID:    account.ID,
		FamilyID:     family.ID,
		RefreshHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		TokenVersion: 1,
		Now:          now,
		ExpiresAt:    now.Add(48 * time.Hour),
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := session.Rotate(now); err != nil {
		t.Fatalf("rotate session: %v", err)
	}
	if err := sessions.Save(ctx, session, 0); err != nil {
		t.Fatalf("save session: %v", err)
	}

	archived, err := sessions.ArchiveExpiredBefore(ctx, now.Add(time.Hour), 10, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if archived.Sessions != 1 || archived.Families != 1 {
		t.Fatalf("archive stats = %+v, want 1/1", archived)
	}
	if _, err := sessions.FindByID(ctx, session.ID); err != nil {
		t.Fatalf("online session must be deleted: %v", err)
	}
	if _, err := sessions.FindFamily(ctx, family.ID); err != nil {
		t.Fatalf("online family must be deleted: %v", err)
	}

	var archivedSessions, archivedFamilies int
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM identity_session_archive WHERE id = ?`, session.ID).Scan(&archivedSessions); err != nil {
		t.Fatalf("count archived sessions: %v", err)
	}
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM identity_session_family_archive WHERE family_id = ?`, family.ID).Scan(&archivedFamilies); err != nil {
		t.Fatalf("count archived families: %v", err)
	}
	if archivedSessions != 1 || archivedFamilies != 1 {
		t.Fatalf("archive rows = %d/%d, want 1/1", archivedSessions, archivedFamilies)
	}
}
