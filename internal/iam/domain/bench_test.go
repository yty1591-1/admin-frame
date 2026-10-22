package domain_test

import (
	"fmt"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
)

// benchPermissionSet 构造 n 个合法权限码的集合；prefix 用于区分前后两套集合。
func benchPermissionSet(prefix string, n int) domain.PermissionSet {
	set := make(domain.PermissionSet, n)
	for i := 0; i < n; i++ {
		set[iam.PermissionCode(fmt.Sprintf("%s:res%d:read", prefix, i))] = struct{}{}
	}
	return set
}

// BenchmarkReplaceRolePermissions_Diff 测量 200 权限整体替换时的差量计算成本。
//
// 对应 docs/rbac-iam.md §13.3「角色权限替换（200 权限）≤ 50ms（不含审计投递）」：
// 每次迭代都是「全量换掉」的最坏情况（200 新增 + 200 撤销），并包含结果排序。
func BenchmarkReplaceRolePermissions_Diff(b *testing.B) {
	current := benchPermissionSet("current", 200)
	next := benchPermissionSet("next", 200)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		change := current.Diff(next)
		if len(change.Added) != 200 || len(change.Removed) != 200 {
			b.Fatalf("unexpected diff: +%d -%d", len(change.Added), len(change.Removed))
		}
	}
}
