package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
)

func TestMemoryRequestRepositoryIdempotency(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRequestRepository()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	n := mustNotification(t, now, "n-1")

	if err := repo.Create(ctx, n); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 同 ID 重复落库 = 重复事件，必须幂等拒绝（消费端吸收而非报错重试）。
	if err := repo.Create(ctx, n); !errors.Is(err, domain.ErrDuplicateRequest) {
		t.Fatalf("duplicate create: want ErrDuplicateRequest, got %v", err)
	}
	got, err := repo.FindByIdempotencyKey(ctx, n.IdempotencyKey)
	if err != nil {
		t.Fatalf("FindByIdempotencyKey: %v", err)
	}
	if got.ID != n.ID {
		t.Fatalf("unexpected lookup result: %+v", got)
	}
	if _, err := repo.FindByIdempotencyKey(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing key: want ErrNotFound, got %v", err)
	}
}

func TestMemoryRequestRepositoryCAS(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRequestRepository()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	n := mustNotification(t, now, "n-1")
	if err := repo.Create(ctx, n); err != nil {
		t.Fatalf("Create: %v", err)
	}

	next, err := n.Transition(domain.RequestDispatched, "", now)
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if err := repo.UpdateStatus(ctx, next); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	// 并发后到者：版本不匹配必须拿到冲突，而不是悄悄覆盖。
	if err := repo.UpdateStatus(ctx, next); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale update: want ErrVersionConflict, got %v", err)
	}
}

func TestMemoryRequestRepositoryListFilter(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRequestRepository()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	a := mustNotification(t, now, "n-a")
	b := mustNotification(t, now, "n-b")
	b.Scene = "tenant.suspend_notice"
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create a: %v", err)
	}
	if err := repo.Create(ctx, b); err != nil {
		t.Fatalf("Create b: %v", err)
	}

	// tenant_id 强制过滤：其他租户查不到。
	items, total, err := repo.List(ctx, domain.RequestFilter{TenantID: "other"})
	if err != nil || total != 0 {
		t.Fatalf("cross-tenant leak: items=%d total=%d err=%v", len(items), total, err)
	}
	items, total, err = repo.List(ctx, domain.RequestFilter{TenantID: "t1", Scene: "tenant.suspend_notice"})
	if err != nil || total != 1 || items[0].ID != b.ID {
		t.Fatalf("scene filter: total=%d err=%v", total, err)
	}
}

// TestRequestRepositoryListRequiresTenant ENT-12：仓储层对空租户键
// fail closed——不信任 app 过滤条件，杜绝全表通知单查询路径。
// MySQL 实现同语义（SQL 构造前同位守卫），需要 DB 的用例归 ENT-16 集成。
func TestRequestRepositoryListRequiresTenant(t *testing.T) {
	repo := NewMemoryRequestRepository()
	_, _, err := repo.List(context.Background(), domain.RequestFilter{})
	if !errors.Is(err, domain.ErrTenantIDRequired) {
		t.Fatalf("empty tenant must be ErrTenantIDRequired, got %v", err)
	}
}

// mustNotification 在本包构造测试通知单（domain 包的 helper 不可跨包引用）。
func mustNotification(t *testing.T, now time.Time, id string) domain.Notification {
	t.Helper()
	n, err := domain.NewNotification(id, "t1", "tenant.invitation", "user-1",
		"notify:tenant.invitation:"+id,
		[]domain.Channel{domain.ChannelEmail, domain.ChannelSms}, "tenant.invitation", "zh-CN",
		domain.PriorityNormal, now.Add(time.Hour), now)
	if err != nil {
		t.Fatalf("NewNotification: %v", err)
	}
	return n
}

func TestMemoryDeliveryRepository(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryDeliveryRepository()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	d, err := domain.NewDelivery("d1", "r1", "t1", domain.ChannelEmail, now)
	if err != nil {
		t.Fatalf("NewDelivery: %v", err)
	}
	if err := repo.Upsert(ctx, d); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := repo.Get(ctx, "r1", domain.ChannelEmail)
	if err != nil || got.ID != "d1" {
		t.Fatalf("Get: %v %+v", err, got)
	}
	if _, err := repo.Get(ctx, "r1", domain.ChannelSms); !errors.Is(err, domain.ErrDeliveryNotFound) {
		t.Fatalf("missing delivery: want ErrDeliveryNotFound, got %v", err)
	}

	// pending → retrying：next_retry_at 必须回填；终态必须有 finished_at。
	retrying, err := d.Transition(domain.DeliveryRetrying, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Transition retrying: %v", err)
	}
	if retrying.NextRetryAt == nil {
		t.Fatal("retrying must set next_retry_at")
	}
	if err := repo.Update(ctx, retrying); err != nil {
		t.Fatalf("Update: %v", err)
	}
	failed, err := retrying.Transition(domain.DeliveryFailed, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Transition failed: %v", err)
	}
	if failed.FinishedAt == nil || failed.NextRetryAt != nil {
		t.Fatalf("terminal delivery timestamps wrong: %+v", failed)
	}
	if err := repo.Update(ctx, failed); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	counts, err := repo.CountByStatus(ctx)
	if err != nil || counts[domain.DeliveryFailed] != 1 {
		t.Fatalf("CountByStatus: %v %+v", err, counts)
	}
}

func TestMemoryTemplateRepositoryVersioning(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryTemplateRepository()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	tpl, err := domain.NewTemplate("tenant.invitation", domain.ChannelEmail, "zh-CN", "s {{a}}", "b {{a}} {{b}}", "admin", now)
	if err != nil {
		t.Fatalf("NewTemplate: %v", err)
	}
	v1, err := repo.Upsert(ctx, tpl)
	if err != nil || v1.Version != 1 {
		t.Fatalf("first upsert: version=%d err=%v", v1.Version, err)
	}
	v2, err := repo.Upsert(ctx, tpl)
	if err != nil || v2.Version != 2 {
		t.Fatalf("second upsert must bump version: version=%d err=%v", v2.Version, err)
	}

	// 停用后 Find 不可见（渲染走不到停用模板）。
	v2.Enabled = false
	if _, err := repo.Upsert(ctx, v2); err != nil {
		t.Fatalf("upsert disabled: %v", err)
	}
	if _, err := repo.Find(ctx, tpl.Key, tpl.Channel, "zh-CN"); !errors.Is(err, domain.ErrTemplateNotFound) {
		t.Fatalf("disabled template must be invisible, got %v", err)
	}
}

func TestMemoryPreferenceAndBlacklist(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	prefRepo := NewMemoryPreferenceRepository()
	p, err := domain.NewPreference("t1", domain.SubjectUser, "u1", domain.ClassMarketing, domain.ChannelEmail, false, now)
	if err != nil {
		t.Fatalf("NewPreference: %v", err)
	}
	if err := prefRepo.Upsert(ctx, p); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := prefRepo.Get(ctx, "t1", domain.SubjectUser, "u1", domain.ClassMarketing, domain.ChannelEmail)
	if err != nil || got.Enabled {
		t.Fatalf("Get preference: %v %+v", err, got)
	}
	if _, err := prefRepo.Get(ctx, "t1", domain.SubjectUser, "u1", domain.ClassOperational, domain.ChannelEmail); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unset preference must be ErrNotFound, got %v", err)
	}

	blackRepo := NewMemoryBlacklistRepository()
	hmac := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	b, err := domain.NewBlacklist(domain.PlatformTenantID, domain.ChannelSms, hmac, domain.BlacklistComplaint, "complaint", "system", now)
	if err != nil {
		t.Fatalf("NewBlacklist: %v", err)
	}
	if err := blackRepo.Add(ctx, b); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// 平台级黑名单对所有租户生效。
	hit, err := blackRepo.Exists(ctx, "t1", domain.ChannelSms, hmac)
	if err != nil || !hit {
		t.Fatalf("platform blacklist must hit tenant lookup: hit=%v err=%v", hit, err)
	}
	if err := blackRepo.Remove(ctx, "t1", domain.ChannelSms, hmac); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("remove tenant-scoped entry must be ErrNotFound, got %v", err)
	}
	if err := blackRepo.Remove(ctx, domain.PlatformTenantID, domain.ChannelSms, hmac); err != nil {
		t.Fatalf("remove platform entry: %v", err)
	}
}
