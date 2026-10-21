package authz_test

import (
	"context"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/authz"
)

// testSnapshot 构造含全部字段的快照，供克隆 / 查询用例复用。
func testSnapshot() authz.Snapshot {
	return authz.Snapshot{
		Channel:     "biz",
		SubjectType: "merchant_staff",
		SubjectID:   "u-1",
		MerchantID:  "m-1",
		Roles:       []string{"merchant_owner", "merchant_ops"},
		Permissions: []string{"order:read", "order:write"},
		Scopes: map[string]authz.Scope{
			"order": {Type: authz.ScopeMerchant, MerchantIDs: nil},
			"*":     {Type: authz.ScopeMerchantSet, MerchantIDs: []string{"m-1", "m-2"}},
		},
		Version:     7,
		GeneratedAt: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC),
	}
}

// TestSnapshotCloneIsolatesSlices 验证克隆快照与源快照完全隔离。
func TestSnapshotCloneIsolatesSlices(t *testing.T) {
	original := testSnapshot()
	clone := original.Clone()

	clone.Roles[0] = "tampered"
	clone.Permissions[0] = "tampered"
	scope := clone.Scopes["*"]
	scope.MerchantIDs[0] = "tampered"
	clone.Scopes["*"] = scope

	if original.Roles[0] != "merchant_owner" || original.Permissions[0] != "order:read" {
		t.Fatalf("clone mutated original slices: roles=%v perms=%v", original.Roles, original.Permissions)
	}
	if got := original.Scopes["*"].MerchantIDs[0]; got != "m-1" {
		t.Fatalf("clone mutated original scope merchant ids: %q", got)
	}
}

// TestSnapshotEmpty 验证空快照判定：仅有数据范围不算有权限。
func TestSnapshotEmpty(t *testing.T) {
	if !(authz.Snapshot{}).Empty() {
		t.Fatal("zero snapshot must be empty")
	}
	withRole := authz.Snapshot{Roles: []string{"ops"}}
	if withRole.Empty() {
		t.Fatal("snapshot with role must not be empty")
	}
	withPermission := authz.Snapshot{Permissions: []string{"order:read"}}
	if withPermission.Empty() {
		t.Fatal("snapshot with permission must not be empty")
	}
	scopesOnly := authz.Snapshot{Scopes: map[string]authz.Scope{"order": {Type: authz.ScopeAll}}}
	if !scopesOnly.Empty() {
		t.Fatal("snapshot without role / permission must be empty")
	}
}

// TestSnapshotHasPermission 验证精确匹配语义：不做通配符展开。
func TestSnapshotHasPermission(t *testing.T) {
	snapshot := testSnapshot()
	if !snapshot.HasPermission("order:read") {
		t.Fatal("order:read must be found")
	}
	if snapshot.HasPermission("order:*") || snapshot.HasPermission("*") {
		t.Fatal("wildcard must not match without exact entry")
	}
}

// TestSnapshotScopeFor 验证资源精确命中与 * 兜底。
func TestSnapshotScopeFor(t *testing.T) {
	snapshot := testSnapshot()
	scope, ok := snapshot.ScopeFor("order")
	if !ok || scope.Type != authz.ScopeMerchant {
		t.Fatalf("order scope=%+v ok=%v", scope, ok)
	}
	fallback, ok := snapshot.ScopeFor("file")
	if !ok || fallback.Type != authz.ScopeMerchantSet || len(fallback.MerchantIDs) != 2 {
		t.Fatalf("fallback scope=%+v ok=%v", fallback, ok)
	}
	fallback.MerchantIDs[0] = "tampered"
	if snapshot.Scopes["*"].MerchantIDs[0] != "m-1" {
		t.Fatal("ScopeFor must return an isolated copy")
	}
	if _, ok := (authz.Snapshot{}).ScopeFor("order"); ok {
		t.Fatal("empty snapshot must not return a scope")
	}
}

// TestSnapshotContextRoundTrip 验证快照在 context 中的写入与读取。
func TestSnapshotContextRoundTrip(t *testing.T) {
	if _, ok := authz.FromContext(context.Background()); ok {
		t.Fatal("empty context must not yield snapshot")
	}
	//nolint:staticcheck // 明确验证 nil context 的防御分支（避免 panic）。
	if _, ok := authz.FromContext(nil); ok {
		t.Fatal("nil context must not yield snapshot")
	}
	snapshot := testSnapshot()
	ctx := authz.WithSnapshot(context.Background(), snapshot)
	got, ok := authz.FromContext(ctx)
	if !ok || got.SubjectID != snapshot.SubjectID || got.Version != snapshot.Version {
		t.Fatalf("snapshot=%+v ok=%v", got, ok)
	}
	//nolint:staticcheck // 验证 WithSnapshot(nil) 不会 panic，退化为 Background。
	got, ok = authz.FromContext(authz.WithSnapshot(nil, snapshot))
	if !ok || got.SubjectID != snapshot.SubjectID {
		t.Fatalf("nil parent ctx snapshot=%+v ok=%v", got, ok)
	}
}
