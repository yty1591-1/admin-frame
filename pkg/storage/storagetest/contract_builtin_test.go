package storagetest_test

import (
	"testing"

	"github.com/admin-frame/admin-frame/pkg/storage"
	"github.com/admin-frame/admin-frame/pkg/storage/storagetest"
)

func TestContractMemory(t *testing.T) {
	s := storage.NewMemory(storage.MemoryConfig{})
	t.Cleanup(func() { _ = s.Close() })
	storagetest.Contract(t, s)
}

func TestContractLocal(t *testing.T) {
	s, err := storage.OpenLocal(storage.LocalConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	storagetest.Contract(t, s)
}

func TestContractPrefixedMemory(t *testing.T) {
	inner := storage.NewMemory(storage.MemoryConfig{})
	t.Cleanup(func() { _ = inner.Close() })
	s, err := storage.WithPrefix(inner, "tenant-x")
	if err != nil {
		t.Fatal(err)
	}
	storagetest.Contract(t, s)
}
