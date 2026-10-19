package storage_test

import (
	"context"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

func TestValidateLifecycleRules(t *testing.T) {
	if err := storage.ValidateLifecycleRules(nil); err != nil {
		t.Fatal(err)
	}
	err := storage.ValidateLifecycleRules([]storage.LifecycleRule{{ID: "a"}})
	if err == nil {
		t.Fatal("expected days required")
	}
	err = storage.ValidateLifecycleRules([]storage.LifecycleRule{
		{ID: "tmp", Prefix: "tmp/", ExpireDays: 7},
		{ID: "tmp", ExpireDays: 1},
	})
	if err == nil {
		t.Fatal("expected duplicate id")
	}
	if err := storage.ValidateLifecycleRules([]storage.LifecycleRule{
		{ID: "tmp", Prefix: "tmp/", ExpireDays: 7},
		{ID: "mpu", AbortIncompleteMultipartDays: 3},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestApplyLifecycleUnsupportedOnMemory(t *testing.T) {
	store := storage.NewMemory(storage.MemoryConfig{})
	defer store.Close()
	err := storage.ApplyLifecycle(context.Background(), store, []storage.LifecycleRule{
		{ID: "tmp", ExpireDays: 1},
	})
	if err == nil {
		t.Fatal("expected unsupported")
	}
}
