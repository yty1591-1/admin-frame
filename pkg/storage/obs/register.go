package obs

import (
	"context"

	"github.com/admin-frame/admin-frame/pkg/storage"
	"github.com/admin-frame/admin-frame/pkg/storage/s3"
)

func init() {
	storage.Register("obs", openOBS)
}

func openOBS(ctx context.Context, cfg storage.Config) (storage.Store, error) {
	if cfg.OBS.Native {
		return openNative(ctx, cfg)
	}
	cfg.Driver = "obs"
	if err := storage.ValidateCloudL1("obs", cfg); err != nil {
		return nil, err
	}
	return s3.Open(ctx, cfg)
}
