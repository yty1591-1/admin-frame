// M2M 机器身份（OAuth2 client_credentials）在真实 MySQL + Redis 上的端到端用例（§21.2）。
//
// 与 internal/identity/app/m2m_test.go 的分工：
//   - 单测用内存仓储覆盖用例分支（scope 越权、宽限、停用）；
//   - 本文件用 SQL 客户端 / 密钥仓储 + Redis 撤销快照，验证「创建 → 签发 → 轮换 →
//     吊销 → 停用」在真实持久化上成立，并另起服务实例证明不依赖进程内状态。
package identity_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	identitycache "github.com/admin-frame/admin-frame/internal/identity/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/jwt"
	"github.com/admin-frame/admin-frame/pkg/outbox"
	"github.com/admin-frame/admin-frame/pkg/redis"
	"github.com/admin-frame/admin-frame/pkg/testkit"
)

// m2mIDs 生成跨运行唯一的主键。
//
// M2M 两张表不在 reset 的清理范围内（历史指纹需保留），固定序号会在
// 同一 MySQL 实例的多次运行之间撞主键；nextID 已按时间戳 + 序号保证唯一。
type m2mIDs struct{}

// NextString 返回下一个唯一主键。
func (m2mIDs) NextString() (string, error) { return nextID(), nil }

// sqlM2MFixture 是按生产装配方式构造的 M2M 端到端夹具。
type sqlM2MFixture struct {
	svc     *app.M2MService
	cfg     app.M2MServiceConfig
	signer  *jwt.Manager
	clients *persistence.SQLM2MClientRepository
	secrets *persistence.SQLM2MSecretRepository
	store   *identitycache.RedisRevocationStore
	clock   *testClock
	sqlDB   *sql.DB
}

// newSQLM2MFixture 用 SQL 仓储、Redis 撤销快照与 Outbox 审计装配 M2M 服务。
func newSQLM2MFixture(t *testing.T, sqlDB *sql.DB, redisClient *redis.Client) *sqlM2MFixture {
	t.Helper()
	clock := &testClock{now: time.Now().UTC()}
	signer, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "https://identity.test",
		Audience: []string{string(domain.ChannelAdmin)},
		Secret:   []byte("01234567890123456789012345678901"),
		TTL:      15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	tokens, err := app.NewTokenService(signer, domain.ChannelAdmin, 15*time.Minute, 168*time.Hour, m2mIDs{},
		app.WithTokenParser(signer),
		app.WithClientTokenTTL(15*time.Minute),
		app.WithTokenClock(clock),
	)
	if err != nil {
		t.Fatalf("new token service: %v", err)
	}
	clients, secrets := persistence.NewSQLM2MRepositories(sqlDB)
	store := identitycache.NewRedisRevocationStore(redisClient.Cache())
	cfg := app.M2MServiceConfig{
		Clients:            clients,
		Secrets:            secrets,
		Tokens:             tokens,
		IDs:                m2mIDs{},
		Audit:              app.OutboxEmitter{Events: outbox.NewMySQLStore(sqlDB)},
		ClientStates:       store,
		RevocationStateTTL: 30 * time.Second,
		Channel:            domain.ChannelAdmin,
		TokenTTL:           15 * time.Minute,
		RotateGrace:        time.Hour,
		MaxSecretAge:       2160 * time.Hour,
		AllowedScopes:      []string{"order:read", "order:write"},
		Service:            "identity-integration",
		Clock:              clock,
	}
	svc, err := app.NewM2MService(cfg)
	if err != nil {
		t.Fatalf("new m2m service: %v", err)
	}
	return &sqlM2MFixture{
		svc: svc, cfg: cfg, signer: signer,
		clients: clients, secrets: secrets, store: store, clock: clock, sqlDB: sqlDB,
	}
}

// restart 用新的仓储实例重建服务，模拟另一个副本或进程重启：
// 校验状态必须全部来自 MySQL / Redis，而不是上一实例的内存。
func (f *sqlM2MFixture) restart(t *testing.T) *app.M2MService {
	t.Helper()
	cfg := f.cfg
	cfg.Clients, cfg.Secrets = persistence.NewSQLM2MRepositories(f.sqlDB)
	svc, err := app.NewM2MService(cfg)
	if err != nil {
		t.Fatalf("restart m2m service: %v", err)
	}
	return svc
}

// newChecker 构造使用真实 MySQL 账号仓储与 Redis 撤销存储的检查器。
func (f *sqlM2MFixture) newChecker(t *testing.T) *app.RevocationChecker {
	t.Helper()
	accounts := persistence.NewSQLAccountRepository(f.sqlDB, persistence.WithSubjectProtector(newIntegrationProtector(t)))
	checker, err := app.NewRevocationChecker(accounts, f.store, app.RevocationFailClosed, 30*time.Second,
		app.WithRevocationClients(f.clients),
		app.WithRevocationClock(f.clock),
	)
	if err != nil {
		t.Fatalf("new revocation checker: %v", err)
	}
	return checker
}

// createClient 创建客户端并注册清理：删除本用例的客户端、密钥与 Redis 快照。
func (f *sqlM2MFixture) createClient(t *testing.T, scopes []string) *app.CreatedM2MClient {
	t.Helper()
	created, err := f.svc.CreateClient(context.Background(), app.CreateM2MClientInput{
		Name:     "billing-worker",
		Owner:    "platform",
		Channels: []domain.Channel{domain.ChannelAdmin},
		Scopes:   scopes,
		ActorID:  "1900000000000000002",
		TraceID:  "trace-m2m-create",
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	clientID := created.Client.ClientID
	t.Cleanup(func() {
		_ = f.store.DeleteClientState(context.Background(), clientID)
		for _, stmt := range []string{
			"DELETE FROM identity_m2m_secret WHERE client_id = ?",
			"DELETE FROM identity_m2m_client WHERE client_id = ?",
		} {
			if _, execErr := f.sqlDB.Exec(stmt, clientID); execErr != nil {
				t.Logf("cleanup %q: %v", stmt, execErr)
			}
		}
	})
	return created
}

// issue 用给定凭据换取机器令牌。
func (f *sqlM2MFixture) issue(t *testing.T, svc *app.M2MService, clientID, secret, scope, traceID string) *app.M2MToken {
	t.Helper()
	token, err := svc.IssueToken(context.Background(), app.IssueTokenInput{
		ClientID: clientID, ClientSecret: secret, Scope: scope, TraceID: traceID,
	})
	if err != nil {
		t.Fatalf("issue token (%s): %v", traceID, err)
	}
	return token
}

// countOutbox 统计指定审计动作写入 async_outbox 的条数。
//
// 只按 action 过滤：同包其它用例会清空 outbox，但不同包可能并行写同一实例，
// 用动作精确匹配避免把别人的事件算进来。
func countOutbox(t *testing.T, sqlDB *sql.DB, action string) int {
	t.Helper()
	var n int
	if err := sqlDB.QueryRow(
		`SELECT COUNT(*) FROM async_outbox WHERE payload LIKE ?`, "%"+action+"%",
	).Scan(&n); err != nil {
		t.Fatalf("count outbox %s: %v", action, err)
	}
	return n
}

// TestM2MClientCredentialsEndToEnd 覆盖 §21.2 的完整生命周期：
// 落库只存摘要 → 签发声明 → 跨副本可认证 → 轮换宽限 → 版本吊销 → 越权拒绝 → 停用拒绝。
func TestM2MClientCredentialsEndToEnd(t *testing.T) {
	sqlDB := testDB(t)
	fixture := newSQLM2MFixture(t, sqlDB, testkit.Redis(t))
	ctx := context.Background()
	checker := fixture.newChecker(t)

	created := fixture.createClient(t, []string{"order:read"})
	clientID := created.Client.ClientID
	if !strings.HasPrefix(clientID, "svc_") || created.ClientSecret == "" {
		t.Fatalf("unexpected credentials: client_id=%q secret_empty=%v", clientID, created.ClientSecret == "")
	}

	// 1. 明文不入库：库里只应有 SHA-256 摘要与展示指纹。
	var storedHash, fingerprint, secretStatus string
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT secret_hash, fingerprint, status FROM identity_m2m_secret WHERE client_id = ?`, clientID,
	).Scan(&storedHash, &fingerprint, &secretStatus); err != nil {
		t.Fatalf("read stored secret: %v", err)
	}
	if storedHash != app.HashM2MSecret(created.ClientSecret) {
		t.Fatalf("secret_hash = %q, want sha256 hex of the issued secret", storedHash)
	}
	if !strings.HasPrefix(fingerprint, "sha256:") || secretStatus != string(domain.M2MSecretCurrent) {
		t.Fatalf("fingerprint=%q status=%q, want sha256: prefix and current", fingerprint, secretStatus)
	}
	var plaintextRows int
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM identity_m2m_secret WHERE secret_hash = ? OR fingerprint = ?`,
		created.ClientSecret, created.ClientSecret,
	).Scan(&plaintextRows); err != nil {
		t.Fatalf("count plaintext rows: %v", err)
	}
	if plaintextRows != 0 {
		t.Fatalf("client_secret must never be stored in plaintext")
	}

	// 2. 签发成功：声明符合 §21.2.3。
	token := fixture.issue(t, fixture.svc, clientID, created.ClientSecret, "order:read", "trace-m2m-issue")
	claims, err := fixture.signer.Parse(token.AccessToken)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if claims.Subject != app.ClientSubject(clientID) {
		t.Fatalf("sub = %q, want client:%s", claims.Subject, clientID)
	}
	if got := claims.GetString("token_use"); got != app.TokenUseClient {
		t.Fatalf("token_use = %q, want client", got)
	}
	if got := claims.GetString("client_id"); got != clientID {
		t.Fatalf("client_id = %q, want %q", got, clientID)
	}
	if got := claims.GetString("scope"); got != "order:read" {
		t.Fatalf("scope = %q, want order:read", got)
	}
	if len(claims.GetStringSlice("roles")) != 0 || claims.GetString("sid") != "" || claims.GetString("merchant_id") != "" {
		t.Fatalf("client token must not carry roles / sid / merchant_id")
	}
	if err := checker.Check(ctx, claims); err != nil {
		t.Fatalf("fresh client token must pass revocation: %v", err)
	}
	// 审计：创建与签发都必须真正落到 Outbox（机器事件无人工主体，actor_id 取 client_id）。
	if got := countOutbox(t, sqlDB, audit.ActionAuthM2MClientCreated); got != 1 {
		t.Fatalf("audit client.created rows = %d, want 1", got)
	}
	if got := countOutbox(t, sqlDB, audit.ActionAuthM2MTokenIssued); got != 1 {
		t.Fatalf("audit token.issued rows = %d, want 1", got)
	}

	// 3. 另一副本用同一 secret 仍可签发：状态来自 MySQL，不是进程内缓存。
	replica := fixture.restart(t)
	if _, err := replica.IssueToken(ctx, app.IssueTokenInput{
		ClientID: clientID, ClientSecret: created.ClientSecret,
		Scope: "order:read", TraceID: "trace-m2m-replica",
	}); err != nil {
		t.Fatalf("issue from another replica: %v", err)
	}

	// 4. 轮换：新 secret 立即生效，旧 secret 在宽限内可用，旧令牌因 ver 落后被吊销。
	rotated, err := fixture.svc.RotateSecret(ctx, clientID, "1900000000000000002", "trace-m2m-rotate")
	if err != nil {
		t.Fatalf("rotate secret: %v", err)
	}
	if rotated.ClientSecret == created.ClientSecret {
		t.Fatalf("rotated secret must differ from the original")
	}
	var current, retiring int
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT
		   SUM(status = 'current'), SUM(status = 'retiring')
		 FROM identity_m2m_secret WHERE client_id = ?`, clientID,
	).Scan(&current, &retiring); err != nil {
		t.Fatalf("count secret status: %v", err)
	}
	if current != 1 || retiring != 1 {
		t.Fatalf("secrets current=%d retiring=%d, want 1/1", current, retiring)
	}
	if err := checker.Check(ctx, claims); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("token issued before rotation must be revoked, got %v", err)
	}

	newToken := fixture.issue(t, fixture.svc, clientID, rotated.ClientSecret, "order:read", "trace-m2m-new-secret")
	newClaims, err := fixture.signer.Parse(newToken.AccessToken)
	if err != nil {
		t.Fatalf("parse rotated token: %v", err)
	}
	if err := checker.Check(ctx, newClaims); err != nil {
		t.Fatalf("token issued after rotation must pass: %v", err)
	}
	// 旧 secret 在 1h 宽限内仍可换令牌（滚动替换窗口）。
	if _, err := fixture.svc.IssueToken(ctx, app.IssueTokenInput{
		ClientID: clientID, ClientSecret: created.ClientSecret,
		Scope: "order:read", TraceID: "trace-m2m-retiring",
	}); err != nil {
		t.Fatalf("retiring secret must work within grace: %v", err)
	}

	// 5. 宽限到期后旧 secret 失效。
	fixture.clock.Advance(2 * time.Hour)
	if _, err := fixture.svc.IssueToken(ctx, app.IssueTokenInput{
		ClientID: clientID, ClientSecret: created.ClientSecret,
		Scope: "order:read", TraceID: "trace-m2m-expired",
	}); !errors.Is(err, app.ErrM2MInvalidClient) {
		t.Fatalf("err = %v, want ErrM2MInvalidClient after grace", err)
	}

	// 6. 越权 scope：客户端只允许 order:read，申请 order:write 必须 invalid_scope。
	if _, err := fixture.svc.IssueToken(ctx, app.IssueTokenInput{
		ClientID: clientID, ClientSecret: rotated.ClientSecret,
		Scope: "order:read order:write", TraceID: "trace-m2m-escalation",
	}); !errors.Is(err, app.ErrM2MInvalidScope) {
		t.Fatalf("err = %v, want ErrM2MInvalidScope", err)
	}
	// 拒绝也要留痕：token.denied 是「谁在试探 scope」的唯一线索。
	if got := countOutbox(t, sqlDB, audit.ActionAuthM2MTokenDenied); got < 1 {
		t.Fatalf("audit token.denied rows = %d, want >= 1", got)
	}

	// 7. 停用：立即停止签发，已签发令牌被撤销检查拒绝。
	if err := fixture.svc.DisableClient(ctx, clientID, "1900000000000000002", "trace-m2m-disable"); err != nil {
		t.Fatalf("disable client: %v", err)
	}
	if _, err := fixture.svc.IssueToken(ctx, app.IssueTokenInput{
		ClientID: clientID, ClientSecret: rotated.ClientSecret,
		Scope: "order:read", TraceID: "trace-m2m-disabled",
	}); !errors.Is(err, app.ErrM2MInvalidClient) {
		t.Fatalf("err = %v, want ErrM2MInvalidClient after disable", err)
	}
	if err := checker.Check(ctx, newClaims); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("token must be revoked after disable, got %v", err)
	}

	// 8. 端别隔离：客户端只允许 admin，biz 端进程不得为其签发。
	bizTokens, err := app.NewTokenService(fixture.signer, domain.ChannelBiz, 15*time.Minute, 168*time.Hour, m2mIDs{},
		app.WithTokenParser(fixture.signer), app.WithClientTokenTTL(15*time.Minute), app.WithTokenClock(fixture.clock))
	if err != nil {
		t.Fatalf("new biz token service: %v", err)
	}
	bizCfg := fixture.cfg
	bizCfg.Tokens = bizTokens
	bizCfg.Channel = domain.ChannelBiz
	bizSvc, err := app.NewM2MService(bizCfg)
	if err != nil {
		t.Fatalf("new biz m2m service: %v", err)
	}
	adminOnly := fixture.createClient(t, []string{"order:read"})
	if _, err := bizSvc.IssueToken(ctx, app.IssueTokenInput{
		ClientID: adminOnly.Client.ClientID, ClientSecret: adminOnly.ClientSecret,
		Scope: "order:read", TraceID: "trace-m2m-channel",
	}); !errors.Is(err, app.ErrM2MInvalidScope) {
		t.Fatalf("err = %v, want ErrM2MInvalidScope for cross-channel client", err)
	}
}
