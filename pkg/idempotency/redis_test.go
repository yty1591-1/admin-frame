package idempotency_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/admin-frame/admin-frame/pkg/idempotency"
	"github.com/admin-frame/admin-frame/pkg/redis"
)

const testTTL = time.Minute

func newRedisStore(t *testing.T) (*idempotency.RedisStore, *redis.Client) {
	t.Helper()
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(server.Close)

	client, err := redis.Open(context.Background(), redis.Config{Addr: server.Addr(), KeyPrefix: "test:iam"})
	if err != nil {
		t.Fatalf("redis.Open: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	store, err := idempotency.NewRedisStore(client)
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	return store, client
}

// TestRedisStoreRequiresClient 验证空客户端直接失败，避免静默退化为无幂等。
func TestRedisStoreRequiresClient(t *testing.T) {
	if _, err := idempotency.NewRedisStore(nil); !errors.Is(err, idempotency.ErrStoreUnavailable) {
		t.Fatalf("err=%v want ErrStoreUnavailable", err)
	}
}

// TestRedisStoreCompleteRoundTrip 验证跨 Store 实例可回放 Completed 记录。
func TestRedisStoreCompleteRoundTrip(t *testing.T) {
	store, client := newRedisStore(t)
	ctx := context.Background()

	record, created, err := store.Begin(ctx, "req-1", "fp-1", testTTL)
	if err != nil || !created {
		t.Fatalf("Begin: created=%v err=%v", created, err)
	}
	if record.State != idempotency.StateInProgress {
		t.Fatalf("state = %v, want in progress", record.State)
	}

	header := http.Header{"Content-Type": []string{"application/json"}}
	if err := store.Complete(ctx, "req-1", "fp-1", 201, header, []byte(`{"ok":true}`), testTTL); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	other, err := idempotency.NewRedisStore(client)
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	replayed, created, err := other.Begin(ctx, "req-1", "fp-1", testTTL)
	if err != nil {
		t.Fatalf("Begin replay: %v", err)
	}
	if created {
		t.Fatal("replay must not create a new record")
	}
	if replayed.State != idempotency.StateCompleted || replayed.StatusCode != http.StatusCreated {
		t.Fatalf("replayed = %+v", replayed)
	}
	if string(replayed.Body) != `{"ok":true}` || replayed.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("replayed body/header = %q / %v", replayed.Body, replayed.Header)
	}
}

// TestRedisStoreConflictAndInProgress 验证指纹冲突与处理中重入的错误语义。
func TestRedisStoreConflictAndInProgress(t *testing.T) {
	store, _ := newRedisStore(t)
	ctx := context.Background()

	if _, created, err := store.Begin(ctx, "req-2", "fp-a", testTTL); err != nil || !created {
		t.Fatalf("Begin: created=%v err=%v", created, err)
	}
	if _, _, err := store.Begin(ctx, "req-2", "fp-b", testTTL); !errors.Is(err, idempotency.ErrConflict) {
		t.Fatalf("err=%v want ErrConflict", err)
	}
	if _, _, err := store.Begin(ctx, "req-2", "fp-a", testTTL); !errors.Is(err, idempotency.ErrInProgress) {
		t.Fatalf("err=%v want ErrInProgress", err)
	}
}

// TestRedisStoreReleaseAllowsRetry 验证释放后可用同一键重新占用。
func TestRedisStoreReleaseAllowsRetry(t *testing.T) {
	store, _ := newRedisStore(t)
	ctx := context.Background()

	if _, created, err := store.Begin(ctx, "req-3", "fp-1", testTTL); err != nil || !created {
		t.Fatalf("Begin: created=%v err=%v", created, err)
	}
	if err := store.Release(ctx, "req-3", "fp-other"); !errors.Is(err, idempotency.ErrConflict) {
		t.Fatalf("release with wrong fingerprint: err=%v want ErrConflict", err)
	}
	if err := store.Release(ctx, "req-3", "fp-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, created, err := store.Begin(ctx, "req-3", "fp-1", testTTL); err != nil || !created {
		t.Fatalf("Begin after release: created=%v err=%v", created, err)
	}
}

// TestRedisStoreCompleteRequiresInProgress 验证未占用的键不能直接完成。
func TestRedisStoreCompleteRequiresInProgress(t *testing.T) {
	store, _ := newRedisStore(t)
	ctx := context.Background()

	if err := store.Complete(ctx, "req-4", "fp-1", 200, nil, nil, testTTL); !errors.Is(err, idempotency.ErrConflict) {
		t.Fatalf("err=%v want ErrConflict", err)
	}
}
