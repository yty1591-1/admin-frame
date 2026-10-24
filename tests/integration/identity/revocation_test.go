// Package identity_test 覆盖身份域在真实 MySQL + Redis 上的撤销与刷新重放语义。
//
// 用例依赖 TEST_MYSQL_DSN / TEST_REDIS_ADDR；未配置时由 testkit 显式跳过，
// 不使用内存实现替代，避免「SQL / Redis 未被验证」的假绿。
package identity_test

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	identitycache "github.com/admin-frame/admin-frame/internal/identity/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/jwt"
	"github.com/admin-frame/admin-frame/pkg/redis"
	"github.com/admin-frame/admin-frame/pkg/testkit"
)

// idSeq 为同一次运行内的主键序号，与时间戳组合保证跨用例、跨运行唯一。
var idSeq int64

// nextID 生成唯一的 BIGINT 主键字符串。
//
// 唯一性不仅为了避免主键冲突，也避免 Redis 撤销快照在多次运行之间残留：
// 快照 TTL 至少 1 小时，固定 ID 会让「停用后拒绝」的断言读到上一轮的 active 状态。
func nextID() string {
	n := atomic.AddInt64(&idSeq, 1)
	return strconv.FormatInt(time.Now().UnixNano()+n, 10)
}

// testDB 返回已迁移的测试数据库并清空身份 / 异步表。
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB := testkit.MySQL(t).SQL()
	reset(t, sqlDB)
	return sqlDB
}

// reset 按外键 RESTRICT 顺序清空身份与异步表。
func reset(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	for _, stmt := range []string{
		"DELETE FROM identity_oauth_identity",
		"DELETE FROM identity_mfa",
		"DELETE FROM identity_login_attempt",
		"DELETE FROM identity_session",
		"DELETE FROM identity_credential",
		"DELETE FROM identity_account",
		"DELETE FROM async_consumed",
		"DELETE FROM async_dlq",
		"DELETE FROM async_outbox",
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("reset %q: %v", stmt, err)
		}
	}
}

// newChecker 构造使用真实 MySQL 账号仓储与 Redis 撤销存储的检查器。
func newChecker(t *testing.T, sqlDB *sql.DB, client *redis.Client) (*app.RevocationChecker, *identitycache.RedisRevocationStore) {
	t.Helper()
	accounts := persistence.NewSQLAccountRepository(sqlDB)
	store := identitycache.NewRedisRevocationStore(client.Cache())
	checker, err := app.NewRevocationChecker(accounts, store, app.RevocationFailClosed, 0)
	if err != nil {
		t.Fatalf("new revocation checker: %v", err)
	}
	return checker, store
}

// seedAccount 落库一个账号并返回领域对象。
func seedAccount(t *testing.T, repo domain.AccountRepository, channel domain.Channel, id, subject string, now time.Time) *domain.Account {
	t.Helper()
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID:      id,
		Channel: channel,
		Subject: subject,
		Now:     now,
	})
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	if err := repo.Save(context.Background(), account, 0); err != nil {
		t.Fatalf("save account: %v", err)
	}
	return account
}

// claimsFor 构造仅含撤销判定所需声明的 claims（签名校验不在本层范围）。
func claimsFor(account *domain.Account, jti string) jwt.Claims {
	return jwt.Claims{
		Subject: account.ID,
		ID:      jti,
		Extra:   map[string]any{"ver": account.TokenVersion},
	}
}

// TestRevocationAfterAccountDisabled 覆盖账号停用后旧访问令牌立即失效。
func TestRevocationAfterAccountDisabled(t *testing.T) {
	sqlDB := testDB(t)
	client := testkit.Redis(t)
	checker, _ := newChecker(t, sqlDB, client)
	repo := persistence.NewSQLAccountRepository(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()

	account := seedAccount(t, repo, domain.ChannelAdmin, nextID(), "disabled-"+nextID()+"@example.com", now)
	claims := claimsFor(account, "jti-"+nextID())

	disabled, err := repo.FindByID(ctx, account.ID)
	if err != nil {
		t.Fatalf("reload account: %v", err)
	}
	if err := disabled.Disable(now); err != nil {
		t.Fatalf("disable account: %v", err)
	}
	if err := repo.Save(ctx, disabled, account.Version); err != nil {
		t.Fatalf("save disabled account: %v", err)
	}

	if err := checker.Check(ctx, claims); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("disabled account must be rejected, got %v", err)
	}
}

// TestRevocationAfterLogoutAll 覆盖 token_version 递增后旧令牌失效、新令牌放行。
func TestRevocationAfterLogoutAll(t *testing.T) {
	sqlDB := testDB(t)
	client := testkit.Redis(t)
	checker, _ := newChecker(t, sqlDB, client)
	repo := persistence.NewSQLAccountRepository(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()

	account := seedAccount(t, repo, domain.ChannelAdmin, nextID(), "logout-all-"+nextID()+"@example.com", now)
	oldClaims := claimsFor(account, "jti-old-"+nextID())

	loaded, err := repo.FindByID(ctx, account.ID)
	if err != nil {
		t.Fatalf("reload account: %v", err)
	}
	if err := loaded.BumpTokenVersion(now); err != nil {
		t.Fatalf("bump token version: %v", err)
	}
	if err := repo.Save(ctx, loaded, account.Version); err != nil {
		t.Fatalf("save account: %v", err)
	}

	if err := checker.Check(ctx, oldClaims); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("stale token version must be rejected, got %v", err)
	}
	if err := checker.Check(ctx, claimsFor(loaded, "jti-new-"+nextID())); err != nil {
		t.Fatalf("current token version must pass, got %v", err)
	}
}

// TestRevocationJTIBlacklist 覆盖 jti 黑名单只影响被拉黑的单个令牌。
func TestRevocationJTIBlacklist(t *testing.T) {
	sqlDB := testDB(t)
	client := testkit.Redis(t)
	checker, store := newChecker(t, sqlDB, client)
	repo := persistence.NewSQLAccountRepository(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()

	account := seedAccount(t, repo, domain.ChannelAdmin, nextID(), "jti-"+nextID()+"@example.com", now)
	revokedJTI := "jti-revoked-" + nextID()
	activeJTI := "jti-active-" + nextID()

	revokedClaims := claimsFor(account, revokedJTI)
	if err := checker.Check(ctx, revokedClaims); err != nil {
		t.Fatalf("token must pass before blacklisting: %v", err)
	}
	if err := store.RevokeToken(ctx, revokedJTI, time.Minute); err != nil {
		t.Fatalf("revoke jti: %v", err)
	}
	if err := checker.Check(ctx, revokedClaims); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("blacklisted jti must be rejected, got %v", err)
	}
	if err := checker.Check(ctx, claimsFor(account, activeJTI)); err != nil {
		t.Fatalf("other jti must still pass, got %v", err)
	}
}

// TestRevocationFailClosed 覆盖 Redis 不可用时 fail_closed 返回 503。
func TestRevocationFailClosed(t *testing.T) {
	sqlDB := testDB(t)
	client := testkit.Redis(t)
	checker, _ := newChecker(t, sqlDB, client)
	repo := persistence.NewSQLAccountRepository(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()

	account := seedAccount(t, repo, domain.ChannelAdmin, nextID(), "fail-closed-"+nextID()+"@example.com", now)

	if err := client.Close(); err != nil {
		t.Fatalf("close redis client: %v", err)
	}
	err := checker.Check(ctx, claimsFor(account, "jti-"+nextID()))
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("fail_closed must return unavailable, got %v", err)
	}
}
