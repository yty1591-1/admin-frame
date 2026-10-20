package cos

import (
	"context"

	"github.com/admin-frame/admin-frame/pkg/storage"
	"github.com/admin-frame/admin-frame/pkg/storage/s3"
)

func init() {
	storage.Register("cos", openCOS)
}

func openCOS(ctx context.Context, cfg storage.Config) (storage.Store, error) {
	if cfg.COS.Native {
		return openNative(ctx, cfg)
	}
	cfg.Driver = "cos"
	if err := storage.ValidateCloudL1("cos", cfg); err != nil {
		return nil, err
	}
	return s3.Open(ctx, cfg)
}
