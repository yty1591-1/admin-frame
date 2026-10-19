package s3

import (
	"context"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

func init() {
	storage.Register("s3", openRegistered)
	storage.Register("minio", openRegistered)
}

func openRegistered(ctx context.Context, cfg storage.Config) (storage.Store, error) {
	return Open(ctx, cfg)
}
