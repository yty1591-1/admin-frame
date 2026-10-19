package platform

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

// StorageConfig 对象存储装配配置（规划 M3/M5；密钥走环境变量）。
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

	// Lifecycle 默认桶生命周期；须 apply_on_start 才写入。
	Lifecycle storage.LifecycleConfig `yaml:"lifecycle"`

	// Buckets 命名附加桶（公有静态 / 私有附件等）；键不可为 default。
	Buckets map[string]BucketConfig `yaml:"buckets"`
}

// BucketConfig 命名桶配置（与顶层 storage 同形，可独立 driver/lifecycle）。
type BucketConfig struct {
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

	Lifecycle storage.LifecycleConfig `yaml:"lifecycle"`
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

// ToStorageConfig 转为 pkg/storage.Config。
func (c BucketConfig) ToStorageConfig() storage.Config {
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

// OpenStorage 按配置打开默认对象存储；driver 空则 memory。
// 忽略 Buckets / Lifecycle 写入（多桶与生命周期用 OpenStorageSet）。
func OpenStorage(ctx context.Context, cfg StorageConfig) (storage.Store, error) {
	if strings.TrimSpace(cfg.Driver) == "" {
		cfg.Driver = "memory"
	}
	return storage.Open(ctx, cfg.ToStorageConfig())
}

// ValidateStorage 校验存储配置与环境互斥（prod 禁止 memory）。
func ValidateStorage(env string, cfg StorageConfig) error {
	if err := validateOneStorage(env, "storage", cfg.Driver, cfg.Local.Root, cfg.Lifecycle); err != nil {
		return err
	}
	for name, b := range cfg.Buckets {
		n := strings.TrimSpace(name)
		if n == "" || strings.EqualFold(n, "default") {
			return fmt.Errorf("storage.buckets name %q is reserved", name)
		}
		label := "storage.buckets." + n
		if err := validateOneStorage(env, label, b.Driver, b.Local.Root, b.Lifecycle); err != nil {
			return err
		}
	}
	return nil
}

func validateOneStorage(env, label, driver, localRoot string, life storage.LifecycleConfig) error {
	d := strings.ToLower(strings.TrimSpace(driver))
	if d == "" {
		d = "memory"
	}
	switch d {
	case "memory", "local", "s3", "minio", "oss", "cos", "obs":
	default:
		return fmt.Errorf("%s.driver unsupported: %s", label, driver)
	}
	if strings.EqualFold(env, "prod") && d == "memory" {
		return fmt.Errorf("%s.driver=memory is forbidden when env=prod", label)
	}
	if d == "local" && localRoot == "" {
		return fmt.Errorf("%s.local.root is required when driver=local", label)
	}
	if err := storage.ValidateLifecycleRules(life.Rules); err != nil {
		return fmt.Errorf("%s.lifecycle: %w", label, err)
	}
	if life.ApplyOnStart && d == "memory" {
		return fmt.Errorf("%s.lifecycle.apply_on_start is unsupported for driver=memory", label)
	}
	if life.ApplyOnStart && d == "local" {
		return fmt.Errorf("%s.lifecycle.apply_on_start is unsupported for driver=local", label)
	}
	return nil
}
