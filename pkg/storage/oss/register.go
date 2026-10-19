package oss

import (
	"context"
	"fmt"

	"github.com/admin-frame/admin-frame/pkg/storage"
	"github.com/admin-frame/admin-frame/pkg/storage/s3"
)

func init() {
	storage.Register("oss", openOSS)
}

func openOSS(ctx context.Context, cfg storage.Config) (storage.Store, error) {
	if cfg.OSS.Native {
		return nil, fmt.Errorf("%w: oss native SDK (L2) not implemented; set native=false for S3-compat", storage.ErrUnsupported)
	}
	cfg.Driver = "oss"
	if err := storage.ValidateCloudL1("oss", cfg); err != nil {
		return nil, err
	}
	return s3.Open(ctx, cfg)
}
