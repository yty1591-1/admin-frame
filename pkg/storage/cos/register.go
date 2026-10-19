package cos

import (
	"context"
	"fmt"

	"github.com/admin-frame/admin-frame/pkg/storage"
	"github.com/admin-frame/admin-frame/pkg/storage/s3"
)

func init() {
	storage.Register("cos", openCOS)
}

func openCOS(ctx context.Context, cfg storage.Config) (storage.Store, error) {
	if cfg.COS.Native {
		return nil, fmt.Errorf("%w: cos native SDK (L2) not implemented; set native=false for S3-compat", storage.ErrUnsupported)
	}
	cfg.Driver = "cos"
	if err := storage.ValidateCloudL1("cos", cfg); err != nil {
		return nil, err
	}
	return s3.Open(ctx, cfg)
}
