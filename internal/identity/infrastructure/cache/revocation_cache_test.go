package cache

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
)

// TestDecodeAccountState 覆盖新格式与旧格式快照解析。
func TestDecodeAccountState(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

	t.Run("new_format", func(t *testing.T) {
		raw := []byte("2|disabled|" + strconv.FormatInt(now.UnixMilli(), 10))
		state, err := decodeState(raw)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if state.Version != 2 || state.Status != domain.AccountStatusDisabled {
			t.Fatalf("unexpected state: %+v", state)
		}
		if !state.FetchedAt.Equal(now) {
			t.Fatalf("fetched_at = %s, want %s", state.FetchedAt, now)
		}
	})

	t.Run("legacy_format_has_zero_fetched_at", func(t *testing.T) {
		state, err := decodeState([]byte("1|active"))
		if err != nil {
			t.Fatalf("decode legacy: %v", err)
		}
		if !state.FetchedAt.IsZero() {
			t.Fatalf("legacy fetched_at must be zero, got %s", state.FetchedAt)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		for _, raw := range []string{"", "1", "0|active", "abc|active", "1|unknown", "1|active|abc"} {
			if _, err := decodeState([]byte(raw)); err == nil {
				t.Fatalf("raw %q must be rejected", raw)
			}
		}
	})
}

// TestRevocationStoreSnapshotRoundTrip 验证内存与 Redis 实现都能保存并删除带 FetchedAt 的快照。
func TestRevocationStoreSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

	stores := map[string]app.RevocationStore{
		"memory": NewMemoryRevocationStore(),
	}
	client, _ := newRedisCache(t)
	stores["redis"] = NewRedisRevocationStore(client)

	for name, store := range stores {
		t.Run(name, func(t *testing.T) {
			state := app.AccountState{Version: 3, Status: domain.AccountStatusDisabled, FetchedAt: now}
			if err := store.SaveAccountState(ctx, "acct-1", state, time.Minute); err != nil {
				t.Fatalf("save: %v", err)
			}
			loaded, hit, err := store.LoadAccountState(ctx, "acct-1")
			if err != nil || !hit {
				t.Fatalf("load hit=%v err=%v", hit, err)
			}
			if loaded.Version != state.Version || loaded.Status != state.Status {
				t.Fatalf("loaded = %+v, want %+v", loaded, state)
			}
			if !loaded.FetchedAt.Equal(now) {
				t.Fatalf("fetched_at = %s, want %s", loaded.FetchedAt, now)
			}
			if err := store.DeleteAccountState(ctx, "acct-1"); err != nil {
				t.Fatalf("delete: %v", err)
			}
			if _, hit, err := store.LoadAccountState(ctx, "acct-1"); err != nil || hit {
				t.Fatalf("after delete hit=%v err=%v", hit, err)
			}
		})
	}
}
