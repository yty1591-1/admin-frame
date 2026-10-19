package platform_test

import (
	"context"
	"testing"

	"github.com/admin-frame/admin-frame/internal/platform"
)

func TestValidateStorageProdForbidsMemory(t *testing.T) {
	err := platform.ValidateStorage("prod", platform.StorageConfig{Driver: "memory"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenStorageMemoryDefault(t *testing.T) {
	store, err := platform.OpenStorage(context.Background(), platform.StorageConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
}

func TestValidateStorageLocalNeedsRoot(t *testing.T) {
	err := platform.ValidateStorage("local", platform.StorageConfig{Driver: "local"})
	if err == nil {
		t.Fatal("expected root required")
	}
}
