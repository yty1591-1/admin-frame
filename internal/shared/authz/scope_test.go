package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/authz"
)

// TestScopeWhereAllows 覆盖四类范围的目标租户判定，含 fail closed 分支。
func TestScopeWhereAllows(t *testing.T) {
	cases := []struct {
		name  string
		where authz.ScopeWhere
		want  map[string]bool
	}{
		{
			name:  "all 放行任意租户",
			where: authz.ScopeWhere{Type: authz.ScopeAll},
			want:  map[string]bool{"m-1": true, "m-9": true, "": false},
		},
		{
			name:  "merchant 只放行本商户",
			where: authz.ScopeWhere{Type: authz.ScopeMerchant, TenantIDs: []string{"m-1"}},
			want:  map[string]bool{"m-1": true, "m-2": false},
		},
		{
			name:  "merchant_set 按集合放行",
			where: authz.ScopeWhere{Type: authz.ScopeMerchantSet, TenantIDs: []string{"m-1", "m-2"}},
			want:  map[string]bool{"m-1": true, "m-2": true, "m-3": false},
		},
		{
			name:  "self 租户界同 merchant",
			where: authz.ScopeWhere{Type: authz.ScopeSelf, TenantIDs: []string{"m-1"}},
			want:  map[string]bool{"m-1": true, "m-2": false},
		},
		{
			name:  "空集合 fail closed",
			where: authz.ScopeWhere{Type: authz.ScopeMerchant},
			want:  map[string]bool{"m-1": false, "": false},
		},
		{
			name:  "未知类型 fail closed",
			where: authz.ScopeWhere{Type: "weird", TenantIDs: []string{"m-1"}},
			want:  map[string]bool{"m-1": false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for tenant, want := range tc.want {
				if got := tc.where.Allows(tenant); got != want {
					t.Fatalf("Allows(%q) = %v, want %v", tenant, got, want)
				}
			}
		})
	}
}

// TestScopeWhereClause 验证 SQL 谓词生成：All 跳过、单值 IN、多值 IN。
func TestScopeWhereClause(t *testing.T) {
	if clause, args := (authz.ScopeWhere{Type: authz.ScopeAll}).Clause("tenant_id"); clause != "" || args != nil {
		t.Fatalf("all must produce empty clause, got %q %v", clause, args)
	}
	clause, args := (authz.ScopeWhere{Type: authz.ScopeMerchant, TenantIDs: []string{"m-1"}}).Clause("tenant_id")
	if clause != "tenant_id IN (?)" || len(args) != 1 || args[0] != "m-1" {
		t.Fatalf("merchant clause mismatch: %q %v", clause, args)
	}
	clause, args = (authz.ScopeWhere{Type: authz.ScopeMerchantSet, TenantIDs: []string{"m-1", "m-2"}}).Clause("merchant_id")
	if clause != "merchant_id IN (?, ?)" || len(args) != 2 {
		t.Fatalf("merchant_set clause mismatch: %q %v", clause, args)
	}
}

// TestSnapshotWhereFallback 验证未配置资源时的回退：biz 收缩到本商户
// （缺 MerchantID 产出空集合），admin 回退 all。
func TestSnapshotWhereFallback(t *testing.T) {
	biz := authz.Snapshot{Channel: "biz", SubjectID: "u-1", MerchantID: "m-1"}
	where := biz.Where("order")
	if where.Type != authz.ScopeMerchant || len(where.TenantIDs) != 1 || where.TenantIDs[0] != "m-1" {
		t.Fatalf("biz fallback mismatch: %+v", where)
	}

	orphan := authz.Snapshot{Channel: "biz", SubjectID: "u-1"}
	where = orphan.Where("order")
	if where.Allows("m-1") {
		t.Fatal("biz without merchant id must deny everything")
	}

	admin := authz.Snapshot{Channel: "admin", SubjectID: "a-1"}
	where = admin.Where("tenant")
	if where.Type != authz.ScopeAll || !where.Allows("m-9") {
		t.Fatalf("admin fallback must be all, got %+v", where)
	}
}

// TestSnapshotWhereResourceOverride 验证资源级配置覆盖回退，且 merchant
// 范围的租户集合以快照 MerchantID 为准（忽略配置漂移）。
func TestSnapshotWhereResourceOverride(t *testing.T) {
	snap := authz.Snapshot{
		Channel:    "admin",
		SubjectID:  "a-1",
		MerchantID: "m-ops",
		Scopes: map[string]authz.Scope{
			"tenant":       {Type: authz.ScopeMerchantSet, MerchantIDs: []string{"m-1", " m-2 "}},
			"notification": {Type: authz.ScopeMerchant, MerchantIDs: []string{"m-elsewhere"}},
			"audit":        {Type: authz.ScopeAll},
		},
	}
	where := snap.Where("tenant")
	if !where.Allows("m-1") || !where.Allows("m-2") || where.Allows("m-3") {
		t.Fatalf("merchant_set must honor configured set: %+v", where)
	}

	where = snap.Where("notification")
	if where.Allows("m-elsewhere") || !where.Allows("m-ops") {
		t.Fatalf("merchant must pin to snapshot merchant id: %+v", where)
	}

	if !snap.Where("audit").Allows("m-9") {
		t.Fatal("configured all must allow any tenant")
	}
}

// TestEnforceMerchant 覆盖端到端判定与哨兵错误。
func TestEnforceMerchant(t *testing.T) {
	snap := authz.Snapshot{Channel: "biz", SubjectID: "u-1", MerchantID: "m-1"}
	if err := snap.EnforceMerchant("order", "m-1"); err != nil {
		t.Fatalf("own tenant must pass: %v", err)
	}
	if err := snap.EnforceMerchant("order", "m-2"); !errors.Is(err, authz.ErrScopeDenied) {
		t.Fatalf("cross tenant must be ErrScopeDenied, got %v", err)
	}
}

// TestEnforceFromContext 验证 context 接入点：缺失放行、存在强校验。
func TestEnforceFromContext(t *testing.T) {
	// 快照缺失：放行（未启用 iam 的部署形态）。
	if err := authz.EnforceFromContext(context.Background(), "tenant", "m-9"); err != nil {
		t.Fatalf("missing snapshot must pass through: %v", err)
	}

	ctx := authz.WithSnapshot(context.Background(), authz.Snapshot{
		Channel: "biz", SubjectID: "u-1", MerchantID: "m-1",
	})
	if err := authz.EnforceFromContext(ctx, "tenant", "m-1"); err != nil {
		t.Fatalf("in-scope target must pass: %v", err)
	}
	if err := authz.EnforceFromContext(ctx, "tenant", "m-2"); !errors.Is(err, authz.ErrScopeDenied) {
		t.Fatalf("out-of-scope target must be ErrScopeDenied, got %v", err)
	}
}
