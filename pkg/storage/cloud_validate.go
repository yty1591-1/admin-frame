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

// CloudNativeParams 从 Config 解析 L2 原生连接参数（厂商段优先，回落 s3）。
type CloudNativeParams struct {
	Bucket    string
	Region    string
	Endpoint  string
	AccessKey string
	SecretKey string
}

// NativeParams 按 driver 提取 L2 参数。
func (c Config) NativeParams(driver string) CloudNativeParams {
	d := normalizeDriver(driver)
	p := CloudNativeParams{
		AccessKey: firstNonEmpty(c.S3.AccessKey),
		SecretKey: firstNonEmpty(c.S3.SecretKey),
		Endpoint:  firstNonEmpty(c.S3.Endpoint),
		Region:    firstNonEmpty(c.S3.Region),
		Bucket:    firstNonEmpty(c.S3.Bucket),
	}
	switch d {
	case "oss":
		p.Bucket = firstNonEmpty(c.OSS.Bucket, p.Bucket)
		p.Region = firstNonEmpty(c.OSS.Region, p.Region)
		p.Endpoint = firstNonEmpty(c.OSS.Endpoint, p.Endpoint)
		p.AccessKey = firstNonEmpty(c.OSS.AccessKey, p.AccessKey)
		p.SecretKey = firstNonEmpty(c.OSS.SecretKey, p.SecretKey)
	case "cos":
		p.Bucket = firstNonEmpty(c.COS.Bucket, p.Bucket)
		p.Region = firstNonEmpty(c.COS.Region, p.Region)
		p.Endpoint = firstNonEmpty(c.COS.Endpoint, p.Endpoint)
		p.AccessKey = firstNonEmpty(c.COS.AccessKey, p.AccessKey)
		p.SecretKey = firstNonEmpty(c.COS.SecretKey, p.SecretKey)
	case "obs":
		p.Bucket = firstNonEmpty(c.OBS.Bucket, p.Bucket)
		p.Region = firstNonEmpty(c.OBS.Region, p.Region)
		p.Endpoint = firstNonEmpty(c.OBS.Endpoint, p.Endpoint)
		p.AccessKey = firstNonEmpty(c.OBS.AccessKey, p.AccessKey)
		p.SecretKey = firstNonEmpty(c.OBS.SecretKey, p.SecretKey)
	}
	return p
}

// ValidateCloudL2 校验云驱动 L2（原生 SDK）必填项。
func ValidateCloudL2(driver string, cfg Config) error {
	d := normalizeDriver(driver)
	switch d {
	case "oss", "cos", "obs":
	default:
		return fmt.Errorf("%w: not a native cloud driver: %s", ErrInvalidConfig, d)
	}
	p := cfg.NativeParams(d)
	if p.Bucket == "" {
		return fmt.Errorf("%w: %s native requires bucket", ErrInvalidConfig, d)
	}
	if p.AccessKey == "" || p.SecretKey == "" {
		return fmt.Errorf("%w: %s native requires access_key and secret_key", ErrInvalidConfig, d)
	}
	switch d {
	case "oss", "cos":
		if p.Region == "" {
			return fmt.Errorf("%w: %s native requires region", ErrInvalidConfig, d)
		}
	case "obs":
		if p.Endpoint == "" {
			return fmt.Errorf("%w: obs native requires endpoint", ErrInvalidConfig)
		}
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
