package iam_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// insertMenu 写入一条菜单，parent 为空串表示根节点。
//
// 仅填充无默认值的列，其余交给 DDL 默认值；id 使用测试保留段，避免与种子冲突。
func insertMenu(ctx context.Context, t *testing.T, id int64, channel, code, parent, routeName string) error {
	t.Helper()
	_, err := testDB(t).ExecContext(ctx, `INSERT INTO iam_menu
		(id, channel, code, parent_code, route_name, title, updated_by, created_at, updated_at)
		VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, 'itest', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))`,
		id, channel, code, parent, routeName, "集成测试菜单")
	return err
}

// TestMenuParentMustStayInSameChannel 验证自引用复合外键把父子节点约束在同一端。
//
// 跨端挂载（如 A 端菜单指向 B 端父节点）必须由数据库拒绝，否则前端两端菜单会串树。
func TestMenuParentMustStayInSameChannel(t *testing.T) {
	ctx := t.Context()
	const (
		adminID = int64(1990000000000000101)
		bizID   = int64(1990000000000000102)
	)

	// 种子中 biz 端存在 member，admin 端存在 system；用 admin 端子节点挂 biz 父节点。
	if err := insertMenu(ctx, t, adminID, "admin", "itest.cross", "member", "ItestCross"); err == nil {
		t.Fatal("跨端父子挂载未被拒绝：期望外键错误")
	}

	// 同端挂载必须成功，随后清理，避免影响其它用例。
	if err := insertMenu(ctx, t, bizID, "biz", "itest.member_child", "member", "ItestMemberChild"); err != nil {
		t.Fatalf("同端父子挂载失败: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testDB(t).ExecContext(context.Background(), "DELETE FROM iam_menu WHERE id = ?", bizID); err != nil {
			t.Fatalf("清理测试菜单失败: %v", err)
		}
	})
}

// TestSQLMenuRepositoryCRUD 验证真实 MySQL 下的创建、读取、乐观锁与唯一键冲突。
func TestSQLMenuRepositoryCRUD(t *testing.T) {
	ctx := t.Context()
	const menuID = int64(1990000000000000201)
	repo := persistence.NewSQLMenuRepository(testDB(t))
	id := strconv.FormatInt(menuID, 10)
	now := time.Now().UTC()
	menu := &domain.Menu{
		ID: id, Channel: domain.ChannelAdmin, Code: "itest.crud", ParentCode: "system",
		NodeType: domain.MenuNodePage, RouteName: "ItestCrud", RoutePath: "crud",
		Component: "/system/role/index.vue", Title: "集成测试菜单", Sort: 90,
		Status: domain.MenuStatusActive, Version: 1, UpdatedBy: "itest",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := menu.Validate(); err != nil {
		t.Fatalf("fixture Validate: %v", err)
	}
	if err := repo.Create(ctx, menu); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testDB(t).ExecContext(context.Background(), "DELETE FROM iam_menu WHERE id = ?", menuID); err != nil {
			t.Fatalf("清理测试菜单失败: %v", err)
		}
	})

	loaded, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loaded.Version != 1 || loaded.Code != "itest.crud" {
		t.Fatalf("loaded = %+v", loaded)
	}

	loaded.Title = "集成测试菜单改名"
	loaded.Version = 2
	loaded.UpdatedAt = time.Now().UTC()
	if err := repo.Update(ctx, loaded, 1); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := repo.Update(ctx, loaded, 1); !hasCode(err, apperr.CodeConflict) {
		t.Fatalf("stale update err = %v, want 40900", err)
	}

	duplicate := *menu
	duplicate.ID = "1990000000000000202"
	duplicate.Title = "重复编码"
	duplicate.RouteName = "ItestOther"
	if err := repo.Create(ctx, &duplicate); !hasCode(err, apperr.CodeConflict) {
		t.Fatalf("duplicate create err = %v, want 40900", err)
	}
}

// hasCode 判断错误是否为指定业务码。
func hasCode(err error, code int) bool {
	var appErr *apperr.AppError
	return errors.As(err, &appErr) && appErr.Code == code
}
