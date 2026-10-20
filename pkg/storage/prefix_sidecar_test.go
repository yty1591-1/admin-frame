package storage_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

type stubCloud struct {
	storage.Store
	lastPresignKey string
	lastMPUKey     string
}

func (s *stubCloud) PresignPut(_ context.Context, key string, _ time.Duration, _ storage.PutOptions) (string, error) {
	s.lastPresignKey = key
	return "https://example/" + key, nil
}

func (s *stubCloud) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	s.lastPresignKey = key
	return "https://example/" + key, nil
}

func (s *stubCloud) CreateMultipart(_ context.Context, key string, _ storage.PutOptions) (string, error) {
	s.lastMPUKey = key
	return "uid", nil
}

func (s *stubCloud) UploadPart(_ context.Context, key, _ string, _ int32, _ io.Reader) (string, error) {
	s.lastMPUKey = key
	return "etag", nil
}

func (s *stubCloud) CompleteMultipart(_ context.Context, key, _ string, _ []storage.CompletedPart) (storage.ObjectInfo, error) {
	s.lastMPUKey = key
	return storage.ObjectInfo{Key: key}, nil
}

func (s *stubCloud) AbortMultipart(_ context.Context, key, _ string) error {
	s.lastMPUKey = key
	return nil
}

func TestPrefixStoreForwardsPresignAndMultipart(t *testing.T) {
	inner := &stubCloud{Store: storage.NewMemory(storage.MemoryConfig{})}
	defer func() { _ = inner.Close() }()
	ps, err := storage.WithPrefix(inner, "env/ch")
	if err != nil {
		t.Fatal(err)
	}
	url, err := ps.PresignPut(context.Background(), "files/a.csv", time.Minute, storage.PutOptions{ContentType: "text/csv"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(url, "env/ch/files/a.csv") || inner.lastPresignKey != "env/ch/files/a.csv" {
		t.Fatalf("url=%s key=%s", url, inner.lastPresignKey)
	}
	uid, err := ps.CreateMultipart(context.Background(), "files/b.bin", storage.PutOptions{})
	if err != nil || uid != "uid" || inner.lastMPUKey != "env/ch/files/b.bin" {
		t.Fatalf("uid=%s key=%s err=%v", uid, inner.lastMPUKey, err)
	}
	if _, ok := storage.AsPresigner(ps); !ok {
		t.Fatal("AsPresigner(prefix)")
	}
	if _, ok := storage.AsMultipart(ps); !ok {
		t.Fatal("AsMultipart(prefix)")
	}
}
