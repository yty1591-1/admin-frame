package storagetest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

// Contract 验证 Store 基本语义：Put/Get/Stat/Exists/List/Delete。
// 调用方负责提供空桶或可隔离前缀的 Store（建议 WithPrefix 测一次）。
func Contract(t *testing.T, store storage.Store) {
	t.Helper()
	if store == nil {
		t.Fatal("store is nil")
	}
	ctx := context.Background()
	key := "contract/hello.txt"
	payload := []byte("hello-storage")

	info, err := store.Put(ctx, key, bytes.NewReader(payload), storage.PutOptions{
		ContentType: "text/plain",
		Metadata:    map[string]string{"case": "contract"},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if info.Key != key {
		t.Fatalf("Put info.key=%q", info.Key)
	}

	st, err := store.Stat(ctx, key)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.Size != int64(len(payload)) {
		t.Fatalf("Stat size=%d", st.Size)
	}

	ok, err := store.Exists(ctx, key)
	if err != nil || !ok {
		t.Fatalf("Exists=%v err=%v", ok, err)
	}

	rc, got, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	body, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("body=%q", body)
	}
	if got.Key != key {
		t.Fatalf("Get key=%q", got.Key)
	}

	list, err := store.List(ctx, storage.ListOptions{Prefix: "contract/", Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, it := range list.Items {
		if it.Key == key {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("List missing key; items=%v", keysOf(list.Items))
	}

	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Stat(ctx, key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("after Delete Stat err=%v", err)
	}
	ok, err = store.Exists(ctx, key)
	if err != nil || ok {
		t.Fatalf("Exists after delete=%v err=%v", ok, err)
	}

	// 非法键
	if _, err := store.Put(ctx, "../x", strings.NewReader("x"), storage.PutOptions{}); !errors.Is(err, storage.ErrInvalidKey) {
		t.Fatalf("traversal Put err=%v", err)
	}
}

func keysOf(items []storage.ObjectInfo) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Key
	}
	return out
}
