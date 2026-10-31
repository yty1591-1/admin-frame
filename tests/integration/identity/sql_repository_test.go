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

// TestSQLAccountSubjectProtection 验证 S5：明文列已删除，读写只走索引与密文。
func TestSQLAccountSubjectProtection(t *testing.T) {
	ctx := context.Background()
	sqlDB := testkit.MySQL(t).SQL()
	protector := newIntegrationProtector(t)
	repo := persistence.NewSQLAccountRepository(sqlDB, persistence.WithSubjectProtector(protector))
	plainRepo := persistence.NewSQLAccountRepository(sqlDB)

	// S5 迁移后账号表不得再有明文 subject 列。
	var subjectColumns int
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM information_schema.columns
		 WHERE table_schema = DATABASE() AND table_name = 'identity_account' AND column_name = 'subject'`,
	).Scan(&subjectColumns); err != nil {
		t.Fatalf("count subject columns: %v", err)
	}
	if subjectColumns != 0 {
		t.Fatalf("identity_account.subject plaintext column still exists")
	}

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

	// 库内 index / ciphertext 非空，且密文不含明文。
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

	// 大小写 / 空白归一化后按索引命中，并解密回填明文。
	found, err := repo.FindBySubject(ctx, domain.ChannelAdmin, "", " sql-subject@example.com ")
	if err != nil {
		t.Fatalf("find by subject via index: %v", err)
	}
	if found.Subject != "sql-subject@example.com" {
		t.Fatalf("decoded subject = %q", found.Subject)
	}

	// 换绑：同步更新索引 / 密文，旧主体不再命中。
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

	// 关键词子串搜索在解密后完成，仍可命中新主体。
	items, total, err := repo.List(ctx, domain.AccountListFilter{
		Channel: domain.ChannelAdmin,
		Keyword: "sql-new",
		Limit:   accountListLimitForTest,
	})
	if err != nil {
		t.Fatalf("list by keyword: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].Subject != "sql-new@example.com" {
		t.Fatalf("keyword list = %+v total=%d", items, total)
	}

	// 未注入保护器的仓储必须失败关闭，禁止退回明文路径。
	if err := plainRepo.Save(ctx, account, 0); err == nil {
		t.Fatal("save without protector must fail closed")
	}
}

// accountListLimitForTest 是集成测试列表分页上限。
const accountListLimitForTest = 20

// TestSQLOAuthSubjectProtection 验证 S5：外部身份主体只存索引与密文。
func TestSQLOAuthSubjectProtection(t *testing.T) {
	ctx := context.Background()
	sqlDB := testkit.MySQL(t).SQL()
	protector := newIntegrationProtector(t)
	accounts := persistence.NewSQLAccountRepository(sqlDB, persistence.WithSubjectProtector(protector))
	oauthRepo := persistence.NewSQLOAuthIdentityRepository(sqlDB, persistence.WithOAuthSubjectProtector(protector))
	plainOAuthRepo := persistence.NewSQLOAuthIdentityRepository(sqlDB)

	var subjectColumns int
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM information_schema.columns
		 WHERE table_schema = DATABASE() AND table_name = 'identity_oauth_identity'
		   AND column_name = 'provider_subject'`,
	).Scan(&subjectColumns); err != nil {
		t.Fatalf("count provider_subject columns: %v", err)
	}
	if subjectColumns != 0 {
		t.Fatalf("identity_oauth_identity.provider_subject plaintext column still exists")
	}

	now := time.Now().UTC()
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID:      "900000000000000301",
		Channel: domain.ChannelC,
		Subject: "oauth-owner@example.com",
		Now:     now,
	})
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	if err := accounts.Save(ctx, account, 0); err != nil {
		t.Fatalf("save account: %v", err)
	}
	identity, err := domain.NewOAuthIdentity(domain.NewOAuthIdentityParams{
		ID:              "900000000000000302",
		AccountID:       account.ID,
		Provider:        "github",
		ProviderSubject: "oauth-subject-1",
		Now:             now,
	})
	if err != nil {
		t.Fatalf("new oauth identity: %v", err)
	}
	if err := oauthRepo.Save(ctx, identity); err != nil {
		t.Fatalf("save oauth identity: %v", err)
	}

	var index []byte
	var ciphertext []byte
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT provider_subject_index, provider_subject_ciphertext
		 FROM identity_oauth_identity WHERE id = ?`, identity.ID).Scan(&index, &ciphertext); err != nil {
		t.Fatalf("read oauth protection columns: %v", err)
	}
	if len(index) == 0 || len(ciphertext) == 0 {
		t.Fatalf("oauth protection columns must be non-empty")
	}
	if bytes.Contains(ciphertext, []byte("oauth-subject-1")) {
		t.Fatalf("oauth ciphertext must not contain plaintext subject")
	}

	found, err := oauthRepo.FindByProviderSubject(ctx, "github", "oauth-subject-1")
	if err != nil {
		t.Fatalf("find oauth identity by subject: %v", err)
	}
	if found.ProviderSubject != "oauth-subject-1" || found.AccountID != account.ID {
		t.Fatalf("found oauth identity = %+v", found)
	}

	if err := plainOAuthRepo.Save(ctx, identity); err == nil {
		t.Fatal("save without protector must fail closed")
	}
}

// TestSQLSessionArchiveSameTransaction 验证归档写入与在线删除在同一事务内生效。
func TestSQLSessionArchiveSameTransaction(t *testing.T) {
	ctx := context.Background()
	sqlDB := testkit.MySQL(t).SQL()
	sessions := persistence.NewSQLSessionRepository(sqlDB)
	accounts := persistence.NewSQLAccountRepository(sqlDB, persistence.WithSubjectProtector(newIntegrationProtector(t)))

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
