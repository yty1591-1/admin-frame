package obs_test

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/storage"
	_ "github.com/admin-frame/admin-frame/pkg/storage/obs"
)

func TestOpenNativeRequiresEndpoint(t *testing.T) {
	_, err := storage.Open(context.Background(), storage.Config{
		Driver: "obs",
		OBS: storage.OBSConfig{
			Native:    true,
			Bucket:    "demo",
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
		Driver: "obs",
		OBS: storage.OBSConfig{
			Native:    true,
			Bucket:    "demo",
			Endpoint:  "obs.cn-north-4.myhuaweicloud.com",
			AccessKey: "AK",
			SecretKey: "SK",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
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
