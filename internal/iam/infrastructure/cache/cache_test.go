package cache_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	iampcache "github.com/admin-frame/admin-frame/internal/iam/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/shared/authz"
	pkgcache "github.com/admin-frame/admin-frame/pkg/cache"
)

// fakeClock 是可控时钟，用于验证 TTL 语义。
type fakeClock struct {
	now time.Time
}

// Now 返回当前伪时间。
func (c *fakeClock) Now() time.Time { return c.now }

// Advance 推进伪时间。
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// newMemory 构造带伪时钟的 L1。
func newMemory(t *testing.T, clock *fakeClock, entries int) *pkgcache.Memory {
	t.Helper()
	return pkgcache.NewMemory(pkgcache.MemoryConfig{MaxEntries: entries, Clock: clock})
}

// testSnapshot 构造可编码的快照。
func testSnapshot() authz.Snapshot {
	return authz.Snapshot{
		Channel:     "biz",
		SubjectType: "merchant_staff",
		SubjectID:   "u-1",
		MerchantID:  "m-1",
		Roles:       []string{"merchant_ops"},
		Permissions: []string{"iam:role:read"},
		Scopes: map[string]authz.Scope{
			"iam": {Type: authz.ScopeMerchant},
		},
		Version:     3,
		GeneratedAt: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC),
	}
}

// TestSubjectKey 验证 key 形态与非法段拒绝。
func TestSubjectKey(t *testing.T) {
	key, err := iampcache.SubjectKey("prod", "biz", "m-1", "merchant_staff", "u-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "prod:biz:m-1:iam:subject:merchant_staff:u-1"
	if key != want {
		t.Fatalf("key=%q want %q", key, want)
	}

	adminKey, err := iampcache.SubjectKey("prod", "admin", "", "staff", "u-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adminKey != "prod:admin:iam:subject:staff:u-2" {
		t.Fatalf("admin key=%q", adminKey)
	}

	cases := []struct {
		name        string
		channel     string
		merchantID  string
		subjectType string
		subjectID   string
	}{
		{name: "missing channel", subjectType: "staff", subjectID: "u"},
		{name: "missing type", channel: "admin", subjectID: "u"},
		{name: "missing subject", channel: "admin", subjectType: "staff"},
		{name: "space in subject", channel: "admin", subjectType: "staff", subjectID: "u 1"},
		{name: "control in merchant", channel: "biz", merchantID: "m\n1", subjectType: "merchant_staff", subjectID: "u"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := iampcache.SubjectKey("prod", tc.channel, tc.merchantID, tc.subjectType, tc.subjectID); !errors.Is(err, iampcache.ErrInvalidConfig) {
				t.Fatalf("err=%v want ErrInvalidConfig", err)
			}
		})
	}
}

// TestEncodeDecodeRoundTrip 验证编码 / 解码字段完整且切片隔离。
func TestEncodeDecodeRoundTrip(t *testing.T) {
	snapshot := testSnapshot()
	raw, err := iampcache.Encode(snapshot)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(string(raw), `"schema_version":1`) {
		t.Fatalf("payload missing schema_version: %s", raw)
	}
	decoded, err := iampcache.Decode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.SubjectID != snapshot.SubjectID || decoded.Channel != snapshot.Channel ||
		decoded.Version != snapshot.Version || !decoded.GeneratedAt.Equal(snapshot.GeneratedAt) {
		t.Fatalf("decoded=%+v", decoded)
	}
	if len(decoded.Permissions) != 1 || decoded.Permissions[0] != "iam:role:read" {
		t.Fatalf("permissions=%v", decoded.Permissions)
	}
	if decoded.Scopes["iam"].Type != authz.ScopeMerchant {
		t.Fatalf("scopes=%v", decoded.Scopes)
	}

	decoded.Permissions[0] = "tampered"
	again, err := iampcache.Decode(raw)
	if err != nil || again.Permissions[0] != "iam:role:read" {
		t.Fatalf("decoded slices must not alias payload: %v %v", again.Permissions, err)
	}
}

// TestDecodeRejectsCorruptPayload 验证脏数据 / 旧 schema 一律报 ErrCorrupt。
func TestDecodeRejectsCorruptPayload(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
	}{
		{name: "empty", raw: nil},
		{name: "not json", raw: []byte("{")},
		{name: "schema mismatch", raw: []byte(`{"schema_version":2,"channel":"admin","subject_id":"u"}`)},
		{name: "missing channel", raw: []byte(`{"schema_version":1,"subject_id":"u"}`)},
		{name: "missing subject", raw: []byte(`{"schema_version":1,"channel":"admin"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := iampcache.Decode(tc.raw); !errors.Is(err, iampcache.ErrCorrupt) {
				t.Fatalf("err=%v want ErrCorrupt", err)
			}
		})
	}
}

// TestNewSnapshotCacheValidation 验证缓存配置校验。
func TestNewSnapshotCacheValidation(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	valid := iampcache.Config{
		L1:          newMemory(t, clock, 8),
		L1TTL:       3 * time.Second,
		L2TTL:       5 * time.Minute,
		NegativeTTL: 30 * time.Second,
	}
	if _, err := iampcache.NewSnapshotCache(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if _, err := iampcache.NewSnapshotCache(iampcache.Config{L1TTL: time.Second, L2TTL: time.Second, NegativeTTL: time.Second}); !errors.Is(err, iampcache.ErrInvalidConfig) {
		t.Fatalf("missing layers err=%v", err)
	}
	noTTL := valid
	noTTL.L2TTL = 0
	if _, err := iampcache.NewSnapshotCache(noTTL); !errors.Is(err, iampcache.ErrInvalidConfig) {
		t.Fatalf("zero ttl err=%v", err)
	}
	negativeTooLarge := valid
	negativeTooLarge.NegativeTTL = 10 * time.Minute
	if _, err := iampcache.NewSnapshotCache(negativeTooLarge); !errors.Is(err, iampcache.ErrInvalidConfig) {
		t.Fatalf("negative_ttl > l2_ttl err=%v", err)
	}
}

// TestSnapshotCacheL1RoundTrip 验证 L1 命中、空 key 与删除。
func TestSnapshotCacheL1RoundTrip(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)}
	l1 := newMemory(t, clock, 8)
	cache, err := iampcache.NewSnapshotCache(iampcache.Config{
		L1: l1, L1TTL: 3 * time.Second, L2TTL: 5 * time.Minute, NegativeTTL: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	key := "prod:biz:m-1:iam:subject:merchant_staff:u-1"
	if _, _, err := cache.Get(context.Background(), key); !errors.Is(err, pkgcache.ErrMiss) {
		t.Fatalf("empty cache err=%v", err)
	}
	if err := cache.Set(context.Background(), key, testSnapshot()); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, layer, err := cache.Get(context.Background(), key)
	if err != nil || layer != iampcache.LayerL1 || got.SubjectID != "u-1" {
		t.Fatalf("get=%+v layer=%v err=%v", got, layer, err)
	}
	// L1 TTL 到期后必须 miss（撤销最坏窗口 3s）。
	clock.Advance(4 * time.Second)
	if _, _, err := cache.Get(context.Background(), key); !errors.Is(err, pkgcache.ErrMiss) {
		t.Fatalf("expired L1 err=%v", err)
	}
	if err := cache.Delete(context.Background(), key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := cache.Set(context.Background(), "", testSnapshot()); !errors.Is(err, iampcache.ErrInvalidConfig) {
		t.Fatalf("empty key set err=%v", err)
	}
	if _, _, err := cache.Get(context.Background(), ""); !errors.Is(err, iampcache.ErrInvalidConfig) {
		t.Fatalf("empty key get err=%v", err)
	}
}

// TestSnapshotCacheL2Backfill 验证 L2 命中回填 L1，且来源层正确。
func TestSnapshotCacheL2Backfill(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)}
	l1 := newMemory(t, clock, 8)
	l2 := newMemory(t, clock, 8)
	cache, err := iampcache.NewSnapshotCache(iampcache.Config{
		L1: l1, L2: l2, L1TTL: 3 * time.Second, L2TTL: 5 * time.Minute, NegativeTTL: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	key := "k"
	if err := l2.Set(context.Background(), key, mustEncode(t, testSnapshot()), 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	got, layer, err := cache.Get(context.Background(), key)
	if err != nil || layer != iampcache.LayerL2 || got.SubjectID != "u-1" {
		t.Fatalf("get=%+v layer=%v err=%v", got, layer, err)
	}
	if _, err := l1.Get(context.Background(), key); err != nil {
		t.Fatalf("L2 hit must backfill L1: %v", err)
	}
}

// TestSnapshotCacheCorruptValue 验证脏值被删除并视为 miss。
func TestSnapshotCacheCorruptValue(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)}
	l1 := newMemory(t, clock, 8)
	cache, err := iampcache.NewSnapshotCache(iampcache.Config{
		L1: l1, L1TTL: 3 * time.Second, L2TTL: 5 * time.Minute, NegativeTTL: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := l1.Set(context.Background(), "bad", []byte("not-json"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cache.Get(context.Background(), "bad"); !errors.Is(err, pkgcache.ErrMiss) {
		t.Fatalf("corrupt value err=%v", err)
	}
	if _, err := l1.Get(context.Background(), "bad"); !errors.Is(err, pkgcache.ErrMiss) {
		t.Fatalf("corrupt value must be deleted: %v", err)
	}
}

// TestSnapshotCacheNegativeTTL 验证空快照使用 negative_ttl。
func TestSnapshotCacheNegativeTTL(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)}
	l1 := newMemory(t, clock, 8)
	cache, err := iampcache.NewSnapshotCache(iampcache.Config{
		L1: l1, L1TTL: 3 * time.Second, L2TTL: 5 * time.Minute, NegativeTTL: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	empty := authz.Snapshot{Channel: "admin", SubjectType: "staff", SubjectID: "u-9"}
	if err := cache.Set(context.Background(), "empty", empty); err != nil {
		t.Fatal(err)
	}
	clock.Advance(31 * time.Second)
	if _, _, err := cache.Get(context.Background(), "empty"); !errors.Is(err, pkgcache.ErrMiss) {
		t.Fatalf("negative entry should expire after 30s: %v", err)
	}
}

// TestSnapshotCacheSoftFailAndDelete 验证 L2 读错误降级、删除失败可观测。
func TestSnapshotCacheSoftFailAndDelete(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)}
	observer := &recordingObserver{}
	l2Err := errors.New("redis down")
	l2 := pkgcache.FuncStore{
		GetFunc: func(context.Context, string) ([]byte, error) { return nil, l2Err },
		SetFunc: func(context.Context, string, []byte, time.Duration) error { return l2Err },
		DeleteFunc: func(context.Context, ...string) error {
			return l2Err
		},
	}
	cache, err := iampcache.NewSnapshotCache(iampcache.Config{
		L1:          newMemory(t, clock, 8),
		L2:          l2,
		L1TTL:       3 * time.Second,
		L2TTL:       5 * time.Minute,
		NegativeTTL: 30 * time.Second,
		Observer:    observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cache.Get(context.Background(), "k"); !errors.Is(err, pkgcache.ErrMiss) {
		t.Fatalf("L2 error must degrade to miss, got %v", err)
	}
	if !observer.has("l2", iampcache.ResultError) {
		t.Fatalf("observer missing l2 error: %+v", observer.entries)
	}
	if err := cache.Set(context.Background(), "k", testSnapshot()); err == nil {
		t.Fatal("L2 set error must be reported")
	}
	if err := cache.Delete(context.Background(), "k"); err == nil {
		t.Fatal("delete error must be reported")
	}
	if !observer.hasInvalidate("l2") {
		t.Fatalf("observer missing invalidate failure: %+v", observer.invalidations)
	}
	if err := cache.Delete(context.Background(), ""); err != nil {
		t.Fatalf("empty key delete must be no-op: %v", err)
	}
	if err := cache.DeleteSubject(context.Background(), "prod", "biz", "m-1", "merchant_staff", "u-1"); err == nil {
		t.Fatal("DeleteSubject must surface L2 failure")
	}
	if err := cache.DeleteSubject(context.Background(), "prod", "", "", "", ""); !errors.Is(err, iampcache.ErrInvalidConfig) {
		t.Fatalf("DeleteSubject invalid key err=%v", err)
	}
}

// TestSnapshotCacheNil 验证 nil 缓存 fail-closed（返回配置错误而非 panic）。
func TestSnapshotCacheNil(t *testing.T) {
	var cache *iampcache.SnapshotCache
	if _, _, err := cache.Get(context.Background(), "k"); !errors.Is(err, iampcache.ErrInvalidConfig) {
		t.Fatalf("nil cache get err=%v", err)
	}
	if err := cache.Set(context.Background(), "k", testSnapshot()); !errors.Is(err, iampcache.ErrInvalidConfig) {
		t.Fatalf("nil cache set err=%v", err)
	}
	if err := cache.Delete(context.Background(), "k"); !errors.Is(err, iampcache.ErrInvalidConfig) {
		t.Fatalf("nil cache delete err=%v", err)
	}
}

// mustEncode 编码快照，失败直接终止用例。
func mustEncode(t *testing.T, snapshot authz.Snapshot) []byte {
	t.Helper()
	raw, err := iampcache.Encode(snapshot)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return raw
}

// recordingObserver 记录观测调用，便于断言。
type recordingObserver struct {
	entries       []string
	invalidations []string
}

func (o *recordingObserver) OnCache(layer iampcache.Layer, result string) {
	o.entries = append(o.entries, string(layer)+":"+result)
}

func (o *recordingObserver) OnInvalidateFailure(layer iampcache.Layer) {
	o.invalidations = append(o.invalidations, string(layer))
}

func (o *recordingObserver) has(layer iampcache.Layer, result string) bool {
	want := string(layer) + ":" + result
	for _, entry := range o.entries {
		if entry == want {
			return true
		}
	}
	return false
}

func (o *recordingObserver) hasInvalidate(layer iampcache.Layer) bool {
	for _, entry := range o.invalidations {
		if entry == string(layer) {
			return true
		}
	}
	return false
}
