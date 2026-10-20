package oss

import (
	"context"

	"github.com/admin-frame/admin-frame/pkg/storage"
	"github.com/admin-frame/admin-frame/pkg/storage/s3"
)

func init() {
	storage.Register("oss", openOSS)
}

func openOSS(ctx context.Context, cfg storage.Config) (storage.Store, error) {
	if cfg.OSS.Native {
		return openNative(ctx, cfg)
	}
	cfg.Driver = "oss"
	if err := storage.ValidateCloudL1("oss", cfg); err != nil {
		return nil, err
	}
	return s3.Open(ctx, cfg)
}
