package platform_test

import (
	"context"
	"testing"

	platformstorage "github.com/admin-frame/admin-frame/internal/platform/storage"
)

func TestValidateStorageProdForbidsMemory(t *testing.T) {
	err := platformstorage.ValidateStorage("prod", platformstorage.Config{Driver: "memory"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenStorageMemoryDefault(t *testing.T) {
	store, err := platformstorage.OpenStorage(context.Background(), platformstorage.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// 忽略：测试清理阶段关闭内存存储，关闭失败不影响断言。
		_ = store.Close()
	})
}

func TestValidateStorageLocalNeedsRoot(t *testing.T) {
	err := platformstorage.ValidateStorage("local", platformstorage.Config{Driver: "local"})
	if err == nil {
		t.Fatal("expected root required")
	}
}
