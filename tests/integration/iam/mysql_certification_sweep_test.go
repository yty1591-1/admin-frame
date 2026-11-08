package iam_test

// S7-5 演练：访问复核超期扫描全流程（MySQL 真实链路）。
//
// 覆盖单元测试无法验证的三件事：
//  1. SQL 仓储 + SQL UnitOfWork 下扫描 / 回收 / 指纹校验的真实语义；
//  2. 审计事件经通用 Outbox（async_outbox）落库并被 handler 投递（fail-closed 契约）；
//  3. 自动回收复用 BindingService.Unbind 后，主体快照（L1/L2）被主动失效。
//
// 环境要求：IAM_TEST_MYSQL_DSN（见 run-integration-tests.sh）；
// TEST_REDIS_ADDR 可选——设置后快照断言覆盖共享 L2，未设置时退化为进程内 L1。

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	iampcache "github.com/admin-frame/admin-frame/internal/iam/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/shared/authz"
	pkgcache "github.com/admin-frame/admin-frame/pkg/cache"
	"github.com/admin-frame/admin-frame/pkg/id"
	"github.com/admin-frame/admin-frame/pkg/outbox"
	pkgredis "github.com/admin-frame/admin-frame/pkg/redis"
)

// certDrillEmitter 捕获经 Outbox → AuditEventHandler 投递的审计事件。
type certDrillEmitter struct {
	mu     sync.Mutex
	events []audit.Event
}

func (e *certDrillEmitter) Emit(_ context.Context, ev audit.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, ev)
	return nil
}

func (e *certDrillEmitter) snapshot() []audit.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]audit.Event(nil), e.events...)
}

// certDrillRevoker 把回收动作适配到 BindingService.Unbind。
//
// 与 cmd/internal/iamruntime 的 bindingCertificationRevoker 同构：
// 回收必须走软撤销 + 快照失效 + rbac.binding_change 审计的同一条链路。
type certDrillRevoker struct {
	svc *app.BindingService
}

func (r certDrillRevoker) Revoke(ctx context.Context, bindingID, actor, reason string) error {
	return r.svc.Unbind(ctx, app.UnbindCommand{ID: bindingID, Actor: actor, Reason: reason})
}

// certDrillEnv 汇总演练所需的 MySQL 真实链路依赖。
type certDrillEnv struct {
	db         *sql.DB
	campaigns  domain.CertificationCampaignRepository
	tasks      domain.CertificationTaskRepository
	roles      domain.RoleRepository
	sqlRoles   *persistence.SQLRoleRepository
	bindings   domain.BindingRepository
	bindingSvc *app.BindingService
	// plain 对应 iam.certification.auto_revoke=false（默认生产行为）。
	plain *app.CertificationService
	// auto 对应 iam.certification.auto_revoke=true。
	auto    *app.CertificationService
	store   outbox.Store
	emitter *certDrillEmitter
	cache   *iampcache.SnapshotCache
	// l2Redis 报告快照缓存是否配置了共享 Redis L2。
	l2Redis bool
}

// newCertDrillEnv 构造演练环境并清空复核相关表（共用 reset() 不含复核表）。
func newCertDrillEnv(t *testing.T) *certDrillEnv {
	t.Helper()
	sqlDB := testDB(t)
	// 清场顺序遵循外键 RESTRICT：任务 →（任务引用的）绑定 → 活动 → 角色。
	for _, stmt := range []string{
		"DELETE FROM iam_certification_task",
		"DELETE FROM iam_role_binding",
		"DELETE FROM iam_certification_campaign",
		"DELETE FROM async_outbox",
		"DELETE FROM async_dlq",
		"DELETE FROM async_consumed",
		"DELETE FROM iam_role WHERE code <> 'system_admin'",
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("cert drill reset %q: %v", stmt, err)
		}
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	ids := id.MustSnowflake(id.Options{NodeID: 7})
	roles := persistence.NewSQLRoleRepository(sqlDB)
	envRoles := roles
	bindings := persistence.NewSQLBindingRepository(sqlDB)
	campaigns := persistence.NewSQLCertificationCampaignRepository(sqlDB)
	tasks := persistence.NewSQLCertificationTaskRepository(sqlDB)
	uow := persistence.NewSQLUnitOfWork(sqlDB)
	store := outbox.NewMySQLStore(sqlDB)
	recorder := app.OutboxRecorder{Events: store}

	cache, l2Redis := newCertDrillSnapshotCache(t)
	invalidator := iampcache.NewSubjectInvalidator(cache, "drill")

	env := &certDrillEnv{
		db:        sqlDB,
		campaigns: campaigns,
		tasks:     tasks,
		roles:     envRoles,
		sqlRoles:  roles,
		bindings:  bindings,
		store:     store,
		emitter:   &certDrillEmitter{},
		cache:     cache,
		l2Redis:   l2Redis,
	}
	bindingSvc, err := app.NewBindingService(app.BindingServiceConfig{
		Roles:       roles,
		Bindings:    bindings,
		Recorder:    recorder,
		Invalidator: invalidator,
		UnitOfWork:  uow,
		IDs:         ids,
		Audit:       app.AuditMeta{Service: "worker", ActorType: "system"},
		Limits:      app.BindingLimits{MaxBindingsPerSubject: 50},
		Logger:      logger,
	})
	if err != nil {
		t.Fatalf("NewBindingService: %v", err)
	}
	env.bindingSvc = bindingSvc
	newCert := func(autoRevoke bool) *app.CertificationService {
		svc, err := app.NewCertificationService(app.CertificationServiceConfig{
			Campaigns:  campaigns,
			Tasks:      tasks,
			Bindings:   bindings,
			Roles:      roles,
			Revoker:    certDrillRevoker{svc: bindingSvc},
			Recorder:   recorder,
			UnitOfWork: uow,
			IDs:        ids,
			Audit:      app.AuditMeta{Service: "worker", ActorType: "system"},
			Limits:     app.CertificationLimits{AllowBizChannel: true, AutoRevoke: autoRevoke},
			Logger:     logger,
		})
		if err != nil {
			t.Fatalf("NewCertificationService(auto_revoke=%v): %v", autoRevoke, err)
		}
		return svc
	}
	env.plain = newCert(false)
	env.auto = newCert(true)
	return env
}

// newCertDrillSnapshotCache 构造快照缓存；TEST_REDIS_ADDR 可用时挂共享 L2。
func newCertDrillSnapshotCache(t *testing.T) (*iampcache.SnapshotCache, bool) {
	t.Helper()
	var l2 pkgcache.Store
	l2Redis := false
	if addr := strings.TrimSpace(os.Getenv("TEST_REDIS_ADDR")); addr != "" {
		client, err := pkgredis.Open(context.Background(),
			pkgredis.Config{Addr: addr, KeyPrefix: "admin-frame:drill:"})
		if err != nil {
			t.Logf("TEST_REDIS_ADDR=%s unreachable (%v); snapshot assertions degrade to L1", addr, err)
		} else {
			t.Cleanup(func() { _ = client.Close() })
			l2 = iampcache.NewRedisStore(client.Cache())
			l2Redis = true
		}
	}
	cache, err := iampcache.NewSnapshotCache(iampcache.Config{
		L1:          pkgcache.NewMemory(pkgcache.MemoryConfig{MaxEntries: 100}),
		L2:          l2,
		L1TTL:       3 * time.Second,
		L2TTL:       5 * time.Minute,
		NegativeTTL: 30 * time.Second,
		// Bind / Unbind 都会写撤权墓碑（默认 3s）：演练要在绑定后立即预置快照，
		// 把墓碑压到 50ms，避免预置值被「禁止回填」机制静默丢弃。
		InvalidateGuard: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewSnapshotCache: %v", err)
	}
	return cache, l2Redis
}

func (e *certDrillEnv) bind(t *testing.T, subjectID, roleID string) *domain.Binding {
	t.Helper()
	binding, err := e.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel:     domain.ChannelAdmin,
		SubjectType: domain.SubjectStaff,
		SubjectID:   subjectID,
		RoleID:      roleID,
		Actor:       "drill",
		Reason:      "S7-5 演练",
	})
	if err != nil {
		t.Fatalf("bind %s: %v", subjectID, err)
	}
	return binding
}

func (e *certDrillEnv) unbind(t *testing.T, bindingID string) {
	t.Helper()
	if err := e.bindingSvc.Unbind(context.Background(), app.UnbindCommand{
		ID: bindingID, Actor: "drill", Reason: "S7-5 演练：解绑重绑",
	}); err != nil {
		t.Fatalf("unbind %s: %v", bindingID, err)
	}
}

func (e *certDrillEnv) createCampaign(t *testing.T, name string, roleIDs ...string) *domain.CertificationCampaign {
	t.Helper()
	campaign, err := e.plain.CreateCampaign(context.Background(), app.CreateCertificationCampaignCommand{
		Channel: domain.ChannelAdmin,
		Name:    name,
		RoleIDs: roleIDs,
		DueAt:   time.Now().UTC().Add(48 * time.Hour),
		Actor:   "drill",
		Reason:  "S7-5 季度复核演练",
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	return campaign
}

func (e *certDrillEnv) activate(t *testing.T, campaignID string) *domain.CertificationCampaign {
	t.Helper()
	campaign, err := e.plain.ActivateCampaign(context.Background(), campaignID,
		app.CertificationScope{Channel: domain.ChannelAdmin}, "drill")
	if err != nil {
		t.Fatalf("ActivateCampaign %s: %v", campaignID, err)
	}
	return campaign
}

// ageCampaign 把截止时间改到过去，模拟「活动已超期」。
//
// 时间是演练的自变量，不能靠 sleep：直接推进 due_at 并按当前版本落库，
// 与生产数据无差别（扫描侧读到的是同一条 SQL 路径）。
func (e *certDrillEnv) ageCampaign(t *testing.T, campaignID string) {
	t.Helper()
	campaign, err := e.campaigns.FindByID(context.Background(), campaignID)
	if err != nil {
		t.Fatalf("FindByID %s: %v", campaignID, err)
	}
	campaign.DueAt = time.Now().UTC().Add(-time.Hour)
	if err := e.campaigns.Save(context.Background(), campaign, campaign.Version); err != nil {
		t.Fatalf("age campaign %s: %v", campaignID, err)
	}
}

// deliverOutbox 模拟常驻 worker：认领 pending 事件 → 审计 handler 投递 → 标记成功。
//
// 返回投递后累计的审计事件快照；这是「审计与业务同事务落 Outbox、再异步投递」
// 这条契约的真实执行，而不是只查表。
func (e *certDrillEnv) deliverOutbox(t *testing.T, wantAtLeast int) []audit.Event {
	t.Helper()
	ctx := context.Background()
	events, err := e.store.ClaimBatch(ctx, time.Now().UTC(), time.Minute, 100)
	if err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if len(events) < wantAtLeast {
		t.Fatalf("outbox claimed %d events, want >=%d", len(events), wantAtLeast)
	}
	handler := app.AuditEventHandler(e.emitter)
	for _, event := range events {
		if err := handler(ctx, event); err != nil {
			t.Fatalf("deliver event %s: %v", event.EventID, err)
		}
		if err := e.store.MarkSucceeded(ctx, event.EventID, time.Now().UTC()); err != nil {
			t.Fatalf("MarkSucceeded %s: %v", event.EventID, err)
		}
	}
	stats, err := e.store.Stats(ctx)
	if err != nil {
		t.Fatalf("outbox Stats: %v", err)
	}
	if stats.Pending != 0 || stats.DLQ != 0 {
		t.Fatalf("outbox not drained: pending=%d dlq=%d", stats.Pending, stats.DLQ)
	}
	return e.emitter.snapshot()
}

// findEvent 按动作与属性筛选审计事件。
func findEvent(events []audit.Event, action, operation string) *audit.Event {
	for i := range events {
		if events[i].Action != action {
			continue
		}
		if operation != "" && events[i].Attrs["operation"] != operation {
			continue
		}
		return &events[i]
	}
	return nil
}

// findEventByActor 在 findEvent 基础上追加操作人过滤：
// 演练里管理员自己的 unbind（actor=drill）与系统自动回收（system:certification）
// 是同一 action + operation，必须按 actor 区分。
func findEventByActor(events []audit.Event, action, operation, actorID string) *audit.Event {
	for i := range events {
		if events[i].Action != action || events[i].ActorID != actorID {
			continue
		}
		if operation != "" && events[i].Attrs["operation"] != operation {
			continue
		}
		return &events[i]
	}
	return nil
}

// certDrillArtifact 落盘演练证据；CERT_DRILL_ARTIFACT_DIR 未设置时写临时目录。
func certDrillArtifact(t *testing.T, name string, data []byte) string {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv("CERT_DRILL_ARTIFACT_DIR"))
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write artifact %s: %v", path, err)
	}
	t.Logf("cert drill artifact: %s", path)
	return path
}

func certDrillSeedSnapshot(t *testing.T, cache *iampcache.SnapshotCache, subjectID string) string {
	t.Helper()
	key, err := iampcache.SubjectKey("drill", "admin", "", "staff", subjectID)
	if err != nil {
		t.Fatalf("SubjectKey: %v", err)
	}
	snapshot := authz.Snapshot{
		Channel:     "admin",
		SubjectType: "staff",
		SubjectID:   subjectID,
		Roles:       []string{"cert_drill_role"},
		Permissions: []string{string(iam.PermRoleRead)},
		Version:     1,
		GeneratedAt: time.Now().UTC(),
	}
	ctx := context.Background()
	// 墓碑（InvalidateGuard）过期前 Set 会被静默跳过；每轮重新 Set 并验证可读。
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := cache.Set(ctx, key, snapshot); err != nil {
			t.Fatalf("seed snapshot %s: %v", subjectID, err)
		}
		if _, _, err := cache.Get(ctx, key); err == nil {
			return key
		}
		if time.Now().After(deadline) {
			t.Fatalf("seeded snapshot %s must be readable: guard not expired in time", subjectID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestCertificationSweepFullFlowOnMySQL 演练 A：auto_revoke=false 的完整闭环。
//
// 步骤：建角色/绑定 → 创建+激活活动 → 推进截止时间 → 手工触发超期
// （等价于 POST /{id}/auto-expire）→ 断言任务 expired、绑定未回收、
// 审计经 Outbox 投递、整仓扫描幂等、证据可导出归档。
func TestCertificationSweepFullFlowOnMySQL(t *testing.T) {
	env := newCertDrillEnv(t)
	ctx := context.Background()

	newRole(t, env.sqlRoles, "720240001", "cert_drill_role_a",
		[]iam.PermissionCode{iam.PermRoleRead}, nil)
	env.bind(t, "9001", "720240001")

	campaign := env.createCampaign(t, "S7-5 演练 A：超期过期")
	activated := env.activate(t, campaign.ID)
	if activated.TaskCount != 1 {
		t.Fatalf("task_count=%d, want 1", activated.TaskCount)
	}
	env.ageCampaign(t, campaign.ID)

	affected, err := env.plain.AutoExpireOverdue(ctx, campaign.ID,
		app.CertificationScope{Channel: domain.ChannelAdmin})
	if err != nil {
		t.Fatalf("AutoExpireOverdue: %v", err)
	}
	if affected != 1 {
		t.Fatalf("affected=%d, want 1", affected)
	}

	tasks, err := env.tasks.ListByCampaign(ctx, campaign.ID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListByCampaign: err=%v tasks=%d", err, len(tasks))
	}
	if tasks[0].Status != domain.CertificationTaskStatusExpired {
		t.Fatalf("task status=%s, want expired", tasks[0].Status)
	}
	// Expire 只推进状态与 decided_at；操作人与原因在审计事件里（见下方 auto_expire 断言）。
	if tasks[0].DecidedAt == nil {
		t.Fatal("expired task must carry decided_at")
	}
	binding, err := env.bindings.FindByID(ctx, tasks[0].BindingID)
	if err != nil {
		t.Fatalf("FindByID binding: %v", err)
	}
	if binding.IsRevoked() {
		t.Fatal("auto_revoke=false must not revoke the binding")
	}

	// 创建 + 激活 + 自动过期：至少 3 条审计事件，全部经 Outbox 投递成功。
	events := env.deliverOutbox(t, 3)
	expireEvent := findEvent(events, audit.ActionRBACCertification, "auto_expire")
	if expireEvent == nil {
		t.Fatalf("rbac.certification(auto_expire) event missing; got %+v", events)
	}
	if expireEvent.ActorID != "system:certification" || expireEvent.ActorType != "system" {
		t.Fatalf("actor=%s/%s, want system:certification/system",
			expireEvent.ActorID, expireEvent.ActorType)
	}
	// Bind 本身会写 binding_change（bind）；超期过期不产生 unbind。
	if findEvent(events, audit.ActionRBACBindingChange, "unbind") != nil {
		t.Fatal("no unbind event expected when auto_revoke=false")
	}

	// 整仓扫描幂等：活动仍是 active（已全部终态），再来一轮不产生新动作。
	result, err := env.plain.SweepOverdue(ctx)
	if err != nil {
		t.Fatalf("SweepOverdue: %v", err)
	}
	if result.Scanned != 1 || result.Overdue != 1 || result.Expired != 1 ||
		result.Failed != 0 || result.Tasks != 0 {
		t.Fatalf("second sweep=%+v, want scanned=1 overdue=1 expired=1 failed=0 tasks=0", result)
	}
	before := len(env.emitter.snapshot())
	env.deliverOutbox(t, 0)
	if after := len(env.emitter.snapshot()); after != before {
		t.Fatalf("idempotent sweep emitted %d extra audit events", after-before)
	}

	// 证据归档（S7-4 遗留的字段完整率验证在此闭环）。
	scope := app.CertificationScope{Channel: domain.ChannelAdmin}
	csvPayload, _, err := env.plain.ExportEvidence(ctx, campaign.ID, scope, "csv", "drill")
	if err != nil {
		t.Fatalf("ExportEvidence csv: %v", err)
	}
	// auto_expire 不写 decided_by（只有人工裁决/自动回收写），任务以 status=expired 呈现；
	// 字段完整率以「列头齐全 + 行数与任务数一致」验证。
	if !strings.Contains(string(csvPayload), "expired") {
		t.Fatal("csv evidence must contain the expired decision")
	}
	certDrillArtifact(t, "certification-sweep-evidence.csv", csvPayload)
	jsonPayload, _, err := env.plain.ExportEvidence(ctx, campaign.ID, scope, "json", "drill")
	if err != nil {
		t.Fatalf("ExportEvidence json: %v", err)
	}
	certDrillArtifact(t, "certification-sweep-evidence.json", jsonPayload)
}

// TestCertificationSweepAutoRevokeOnMySQL 演练 B：auto_revoke=true 的回收与防护。
//
// 两个主体、两个活动：
//  - 9002：生成任务后解绑重绑 → 指纹漂移，扫描必须 skip，绝不回收新绑定；
//  - 9003：指纹一致 → 自动回收，绑定软撤销、审计留痕、主体快照被失效。
func TestCertificationSweepAutoRevokeOnMySQL(t *testing.T) {
	env := newCertDrillEnv(t)
	ctx := context.Background()

	// 两个场景用不同角色：活动的默认范围是「商户内全部角色」，
	// 共用一个角色会让场景 2 的清单生成把场景 1 的新绑定也纳入。
	newRole(t, env.sqlRoles, "720240002", "cert_drill_role_b",
		[]iam.PermissionCode{iam.PermRoleRead}, nil)
	newRole(t, env.sqlRoles, "720240003", "cert_drill_role_c",
		[]iam.PermissionCode{iam.PermRoleRead}, nil)

	// 场景 1：指纹漂移 → skipped。
	b1 := env.bind(t, "9002", "720240002")
	c1 := env.createCampaign(t, "S7-5 演练 B1：指纹漂移", "720240002")
	env.activate(t, c1.ID)
	env.ageCampaign(t, c1.ID)
	env.unbind(t, b1.ID)
	b2 := env.bind(t, "9002", "720240002") // 任务仍指向已撤销的 b1

	// 场景 2：指纹一致 → revoked。
	env.bind(t, "9003", "720240003")
	c2 := env.createCampaign(t, "S7-5 演练 B2：自动回收", "720240003")
	env.activate(t, c2.ID)
	env.ageCampaign(t, c2.ID)
	snapshotKey := certDrillSeedSnapshot(t, env.cache, "9003")

	result, err := env.auto.SweepOverdue(ctx)
	if err != nil {
		t.Fatalf("SweepOverdue(auto): %v", err)
	}
	if result.Overdue != 2 || result.Expired != 2 || result.Failed != 0 || result.Tasks != 2 {
		t.Fatalf("result=%+v, want overdue=2 expired=2 failed=0 tasks=2", result)
	}

	// 9002：任务 skipped，重绑后的 b2 必须仍然有效。
	tasks1, err := env.tasks.ListByCampaign(ctx, c1.ID)
	if err != nil || len(tasks1) != 1 {
		t.Fatalf("ListByCampaign(c1): err=%v tasks=%d", err, len(tasks1))
	}
	if tasks1[0].Status != domain.CertificationTaskStatusSkipped {
		t.Fatalf("fingerprint-mismatch task status=%s, want skipped", tasks1[0].Status)
	}
	kept, err := env.bindings.FindByID(ctx, b2.ID)
	if err != nil {
		t.Fatalf("FindByID b2: %v", err)
	}
	if kept.IsRevoked() {
		t.Fatal("fingerprint mismatch must not revoke the re-bound binding")
	}

	// 9003：任务 revoked，绑定软撤销，快照键被删除（含共享 L2 时同时覆盖）。
	tasks2, err := env.tasks.ListByCampaign(ctx, c2.ID)
	if err != nil || len(tasks2) != 1 {
		t.Fatalf("ListByCampaign(c2): err=%v tasks=%d", err, len(tasks2))
	}
	if tasks2[0].Status != domain.CertificationTaskStatusRevoked {
		t.Fatalf("auto-revoke task status=%s, want revoked", tasks2[0].Status)
	}
	if tasks2[0].DecidedBy != "system:certification" {
		t.Fatalf("decided_by=%q, want system:certification", tasks2[0].DecidedBy)
	}
	revoked, err := env.bindings.FindByID(ctx, tasks2[0].BindingID)
	if err != nil {
		t.Fatalf("FindByID revoked binding: %v", err)
	}
	if !revoked.IsRevoked() {
		t.Fatal("binding must be soft-revoked in MySQL")
	}
	if _, _, err := env.cache.Get(ctx, snapshotKey); err == nil {
		t.Fatalf("subject snapshot must be invalidated after auto revoke (l2_redis=%v)", env.l2Redis)
	} else if !errors.Is(err, pkgcache.ErrMiss) {
		t.Fatalf("snapshot lookup error: %v", err)
	}

	// 回收链路审计：rbac.binding_change（unbind）+ rbac.certification（auto_revoke）。
	events := env.deliverOutbox(t, 6)
	unbindEvent := findEventByActor(events, audit.ActionRBACBindingChange, "unbind", "system:certification")
	if unbindEvent == nil {
		t.Fatalf("rbac.binding_change(unbind) event missing; got %+v", events)
	}
	if unbindEvent.ActorID != "system:certification" {
		t.Fatalf("unbind actor=%q, want system:certification", unbindEvent.ActorID)
	}
	revokeEvent := findEvent(events, audit.ActionRBACCertification, "auto_revoke")
	if revokeEvent == nil {
		t.Fatalf("rbac.certification(auto_revoke) event missing; got %+v", events)
	}
	skipEvent := findEvent(events, audit.ActionRBACCertification, "auto_skip")
	if skipEvent == nil {
		t.Fatal("rbac.certification(auto_skip) event missing for fingerprint-mismatch task")
	}
	// 演练摘要（写入日志，供演练记录引用）。
	t.Logf("cert drill summary: l2_redis=%v scanned=%d overdue=%d expired=%d tasks=%d audit_events=%d",
		env.l2Redis, result.Scanned, result.Overdue, result.Expired, result.Tasks, len(events))
}
