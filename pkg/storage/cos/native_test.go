package cos_test

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/storage"
	_ "github.com/admin-frame/admin-frame/pkg/storage/cos"
)

func TestOpenNativeRequiresRegion(t *testing.T) {
	_, err := storage.Open(context.Background(), storage.Config{
		Driver: "cos",
		COS: storage.COSConfig{
			Native:    true,
			Bucket:    "demo-1250000000",
			AccessKey: "AK",
			SecretKey: "SK",
		},
	})
	if err == nil || !errors.Is(err, storage.ErrInvalidConfig) {
		t.Fatalf("err=%v", err)
	}
}

func TestOpenNativeConstructsClient(t *testing.T) {
	store, err := storage.Open(context.Background(), storage.Config{
		Driver: "cos",
		COS: storage.COSConfig{
			Native:    true,
			Bucket:    "demo-1250000000",
			Region:    "ap-guangzhou",
			AccessKey: "AK",
			SecretKey: "SK",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, ok := storage.AsPresigner(store); !ok {
		t.Fatal("expected Presigner")
	}
	if _, ok := storage.AsMultipart(store); !ok {
		t.Fatal("expected Multipart")
	}
	if _, ok := storage.AsLifecycle(store); !ok {
		t.Fatal("expected Lifecycle")
	}
}
