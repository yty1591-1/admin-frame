package storage

import (
	"fmt"
	"strings"
)

// ValidateCloudL1 校验云驱动 L1（S3 兼容）必填项。
func ValidateCloudL1(driver string, cfg Config) error {
	d := normalizeDriver(driver)
	sc := cfg.EffectiveS3()
	switch d {
	case "s3":
		if sc.Bucket == "" || sc.Region == "" {
			return fmt.Errorf("%w: s3 requires bucket and region", ErrInvalidConfig)
		}
	case "minio":
		if sc.Bucket == "" || sc.Endpoint == "" {
			return fmt.Errorf("%w: minio requires bucket and endpoint", ErrInvalidConfig)
		}
	case "oss", "cos", "obs":
		if sc.Bucket == "" {
			return fmt.Errorf("%w: %s requires bucket", ErrInvalidConfig, d)
		}
		if strings.TrimSpace(sc.Endpoint) == "" {
			return fmt.Errorf("%w: %s L1 requires endpoint (vendor or s3.endpoint)", ErrInvalidConfig, d)
		}
		if sc.Region == "" {
			return fmt.Errorf("%w: %s requires region", ErrInvalidConfig, d)
		}
	default:
		return fmt.Errorf("%w: not a cloud driver: %s", ErrInvalidConfig, d)
	}
	return nil
}
