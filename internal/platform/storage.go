package platform

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

// StorageConfig 对象存储装配配置（规划 M3；密钥走环境变量）。
type StorageConfig struct {
	Driver         string        `yaml:"driver"`
	KeyPrefix      string        `yaml:"key_prefix"`
	MaxObjectBytes int64         `yaml:"max_object_bytes"`
	PresignPutTTL  time.Duration `yaml:"presign_put_ttl"`
	PresignGetTTL  time.Duration `yaml:"presign_get_ttl"`

	Local storage.LocalConfig `yaml:"local"`
	S3    storage.S3Config    `yaml:"s3"`
	OSS   storage.OSSConfig   `yaml:"oss"`
	COS   storage.COSConfig   `yaml:"cos"`
	OBS   storage.OBSConfig   `yaml:"obs"`
}

// ToStorageConfig 转为 pkg/storage.Config。
func (c StorageConfig) ToStorageConfig() storage.Config {
	return storage.Config{
		Driver:         c.Driver,
		KeyPrefix:      c.KeyPrefix,
		MaxObjectBytes: c.MaxObjectBytes,
		PresignPutTTL:  c.PresignPutTTL,
		PresignGetTTL:  c.PresignGetTTL,
		Local:          c.Local,
		S3:             c.S3,
		OSS:            c.OSS,
		COS:            c.COS,
		OBS:            c.OBS,
	}
}

// OpenStorage 按配置打开对象存储；driver 空则 memory。
func OpenStorage(ctx context.Context, cfg StorageConfig) (storage.Store, error) {
	if strings.TrimSpace(cfg.Driver) == "" {
		cfg.Driver = "memory"
	}
	return storage.Open(ctx, cfg.ToStorageConfig())
}

// ValidateStorage 校验存储配置与环境互斥（prod 禁止 memory）。
func ValidateStorage(env string, cfg StorageConfig) error {
	d := strings.ToLower(strings.TrimSpace(cfg.Driver))
	if d == "" {
		d = "memory"
	}
	switch d {
	case "memory", "local", "s3", "minio", "oss", "cos", "obs":
	default:
		return fmt.Errorf("unsupported storage.driver: %s", cfg.Driver)
	}
	if strings.EqualFold(env, "prod") && d == "memory" {
		return fmt.Errorf("storage.driver=memory is forbidden when env=prod")
	}
	if d == "local" && cfg.Local.Root == "" {
		return fmt.Errorf("storage.local.root is required when driver=local")
	}
	return nil
}
