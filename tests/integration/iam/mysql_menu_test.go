package iam_test

import (
	"context"
	"testing"
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
