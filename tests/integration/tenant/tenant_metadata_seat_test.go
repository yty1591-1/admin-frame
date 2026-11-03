package tenant_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
)

// uniqIDs 生成带唯一前缀的 ID：集成用例共享同一个库，seqIDs 从 t1 起计数
// 会与其它用例的残留行撞主键。
type uniqIDs struct {
	prefix string
	n      int
}

func (g *uniqIDs) Next() (int64, error) {
	g.n++
	return int64(g.n), nil
}

func (g *uniqIDs) NextString() (string, error) {
	g.n++
	return fmt.Sprintf("%s-%d-%d", g.prefix, time.Now().UnixNano(), g.n), nil
}

// TestMetadataRoundTripOnMySQL 验证元数据在真实库中的写入与回读（T-11）。
//
// 覆盖点：显式编码 / 默认编码（code=id）、Profile 全字段 CAS 写入、
// NULL 元数据回读为空串。
func TestMetadataRoundTripOnMySQL(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("NewMySQLRepository() error = %v", err)
	}
	service, err := app.NewService(repo, &uniqIDs{prefix: uniqueID(t)}, quietEmitter{}, app.Options{
		Service: "admin-api", Plans: repo, Members: repo, Quotas: repo,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	created, err := service.Create(ctx, app.CreateInput{
		Name: "元数据集成租户", Code: "it-meta-" + uniqueID(t)[3:], Actor: itActor(),
	})
	if err != nil {
		t.Fatalf("Create(code) error = %v", err)
	}
	if created.Code == "" || created.Code == created.ID {
		t.Fatalf("显式编码应生效, got %q", created.Code)
	}
	t.Cleanup(func() { _, _ = sqlDB.Exec("DELETE FROM tenant WHERE id = ?", created.ID) })

	name := "元数据改名"
	updated, err := service.UpdateProfile(ctx, app.UpdateProfileInput{
		ID: created.ID, Name: &name,
		ContactName:  strPtr("张三"),
		ContactEmail: strPtr("ops@acme.com"),
		Industry:     strPtr("制造"),
		Timezone:     strPtr("Asia/Shanghai"),
		Locale:       strPtr("zh-CN"),
		ExternalID:   strPtr("crm-42"),
		Actor:        itActor(),
	})
	if err != nil {
		t.Fatalf("UpdateProfile() error = %v", err)
	}

	reloaded, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if reloaded.Code != created.Code || reloaded.Name != name ||
		reloaded.ContactName != "张三" || reloaded.ContactEmail != "ops@acme.com" ||
		reloaded.Industry != "制造" || reloaded.Timezone != "Asia/Shanghai" ||
		reloaded.Locale != "zh-CN" || reloaded.ExternalID != "crm-42" {
		t.Fatalf("回读不一致: %+v", reloaded)
	}
	if reloaded.Version != updated.Version {
		t.Fatalf("Version = %d, want %d", reloaded.Version, updated.Version)
	}

	// 清空语义：显式空串写 NULL。
	empty := ""
	if _, err := service.UpdateProfile(ctx, app.UpdateProfileInput{
		ID: created.ID, ExternalID: &empty, Actor: itActor(),
	}); err != nil {
		t.Fatalf("UpdateProfile(clear) error = %v", err)
	}
	reloaded, err = repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get(after clear) error = %v", err)
	}
	if reloaded.ExternalID != "" || reloaded.Industry != "制造" {
		t.Fatalf("清空语义错误: %+v", reloaded)
	}

	// 重复编码 → 409。
	if _, err := service.Create(ctx, app.CreateInput{
		Name: "撞码", Code: created.Code, Actor: itActor(),
	}); err == nil {
		t.Fatal("重复编码应返回错误")
	} else {
		var appErr *apperr.AppError
		if !errors.As(err, &appErr) || appErr.Code != apperr.CodeConflict {
			t.Fatalf("重复编码 error = %v, want 409", err)
		}
	}
}

// TestDefaultCodeBackfillOnMySQL 验证未提供编码时以租户 ID 兜底落库（与迁移 000013 同源）。
func TestDefaultCodeBackfillOnMySQL(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("NewMySQLRepository() error = %v", err)
	}
	service, err := app.NewService(repo, &uniqIDs{prefix: uniqueID(t)}, quietEmitter{}, app.Options{
		Service: "admin-api", Plans: repo, Members: repo, Quotas: repo,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	created, err := service.Create(ctx, app.CreateInput{Name: "兜底编码", Actor: itActor()})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Code != created.ID {
		t.Fatalf("Code = %q, want %q", created.Code, created.ID)
	}
	t.Cleanup(func() { _, _ = sqlDB.Exec("DELETE FROM tenant WHERE id = ?", created.ID) })
	var code string
	if err := sqlDB.QueryRowContext(ctx, `SELECT code FROM tenant WHERE id = ?`, created.ID).Scan(&code); err != nil {
		t.Fatalf("scan code error = %v", err)
	}
	if code != created.ID {
		t.Fatalf("库中 code = %q, want %q", code, created.ID)
	}
}

// TestSeatLimitConcurrencyOnMySQL 验证并发添加在真实行锁下只有一个胜出（T-13）。
//
// 配额 user=2（owner 已占 1 席）：4 个并发添加只应成功 1 个，其余 409——
// 证明席位校验确实运行在租户行锁的临界区内，而不是「先读后写」的竞态。
func TestSeatLimitConcurrencyOnMySQL(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("NewMySQLRepository() error = %v", err)
	}
	service, err := app.NewService(repo, &uniqIDs{prefix: uniqueID(t)}, quietEmitter{}, app.Options{
		Service: "admin-api", Plans: repo, Members: repo, Quotas: repo,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	members, err := app.NewMemberService(repo, repo, quietEmitter{}, app.Options{
		Service: "admin-api", Plans: repo, Members: repo, Quotas: repo,
	})
	if err != nil {
		t.Fatalf("NewMemberService() error = %v", err)
	}

	created, err := service.Create(ctx, app.CreateInput{
		Name: "席位并发", OwnerSubjectID: "it-owner", Actor: itActor(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec("DELETE FROM tenant_member WHERE tenant_id = ?", created.ID)
		_, _ = sqlDB.Exec("DELETE FROM tenant WHERE id = ?", created.ID)
	})
	quotas, err := app.NewQuotaService(repo, repo, quietEmitter{}, app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewQuotaService() error = %v", err)
	}
	if _, err := quotas.Set(ctx, app.SetQuotaInput{
		TenantID: created.ID, Resource: "user", Limit: 2, Actor: itActor(),
	}); err != nil {
		t.Fatalf("Set(user=2) error = %v", err)
	}

	const contenders = 4
	var (
		wg        sync.WaitGroup
		successes atomic.Int32
		conflicts atomic.Int32
		start     = make(chan struct{})
	)
	for i := range contenders {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			_, err := members.Add(ctx, app.AddMemberInput{
				TenantID: created.ID, SubjectID: fmt.Sprintf("u-%d", n),
				Role: domain.MemberRoleMember, Actor: itActor(),
			})
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, domain.ErrSeatLimitExceeded):
				conflicts.Add(1)
			default:
				t.Errorf("并发添加意外错误: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if successes.Load() != 1 || conflicts.Load() != contenders-1 {
		t.Fatalf("success=%d conflicts=%d, want 1/%d", successes.Load(), conflicts.Load(), contenders-1)
	}
	active, err := repo.CountActive(ctx, created.ID)
	if err != nil {
		t.Fatalf("CountActive() error = %v", err)
	}
	if active != 2 {
		t.Fatalf("在册成员 = %d, want 2", active)
	}
}

// itActor 是集成测试操作者（与既有用例同口径）。
func itActor() app.Actor {
	return app.Actor{SubjectID: "it-admin", ActorType: "user", Channel: "admin", TraceID: "it-trace"}
}

// strPtr 小包装。
func strPtr(v string) *string { return &v }
