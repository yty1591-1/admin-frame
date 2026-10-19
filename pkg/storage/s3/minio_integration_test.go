//go:build minio

package s3_test

import (
	"context"
	"os"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/storage"
	_ "github.com/admin-frame/admin-frame/pkg/storage/s3"
	"github.com/admin-frame/admin-frame/pkg/storage/storagetest"
)

// 联调：docker compose -f docker-compose.storage.yml up -d
// 然后：go test -tags=minio ./pkg/storage/s3/ -count=1
func TestMinIOContract(t *testing.T) {
	endpoint := envOr("STORAGE_MINIO_ENDPOINT", "http://127.0.0.1:9000")
	access := envOr("STORAGE_MINIO_ACCESS_KEY", "minioadmin")
	secret := envOr("STORAGE_MINIO_SECRET_KEY", "minioadmin")
	bucket := envOr("STORAGE_MINIO_BUCKET", "admin-frame")

	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{
		Driver: "minio",
		S3: storage.S3Config{
			Endpoint:       endpoint,
			Region:         "us-east-1",
			Bucket:         bucket,
			AccessKey:      access,
			SecretKey:      secret,
			ForcePathStyle: true,
			DisableSSL:     true,
		},
		KeyPrefix: "contract-test/",
	})
	if err != nil {
		t.Fatalf("Open minio: %v (is compose up?)", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	storagetest.Contract(t, store)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
