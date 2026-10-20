package platform

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
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
	// 命名桶内不允许再次声明 buckets（ValidateStorage 会拒绝）。
	Buckets map[string]StorageConfig `yaml:"buckets"`
}

// BucketConfig 是命名桶配置的类型别名。
//
// 与顶层 storage 完全同形，只是语义上位于 storage.buckets.<name>；
// 复用同一类型可保证字段映射只有一份实现，新增字段不会漏改（PLAT-09）。
type BucketConfig = StorageConfig

// LogValue 实现 slog.LogValuer：只输出非敏感字段。
//
// 各云厂商的 access_key / secret_key / session_token 不进入日志（SEC-05）。
func (c StorageConfig) LogValue() slog.Value {
	names := make([]string, 0, len(c.Buckets))
	for name := range c.Buckets {
		names = append(names, name)
	}
	sort.Strings(names)
	return slog.GroupValue(
		slog.String("driver", c.Driver),
		slog.String("key_prefix", c.KeyPrefix),
		slog.Int64("max_object_bytes", c.MaxObjectBytes),
		slog.Bool("lifecycle_apply_on_start", c.Lifecycle.ApplyOnStart),
		slog.Any("buckets", names),
	)
}

// ToStorageConfig 转为 pkg/storage.Config。
//
// 只映射连接参数；Buckets / Lifecycle 由 OpenStorageSet 单独处理。
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
		if len(b.Buckets) > 0 {
			return fmt.Errorf("%s.buckets is not supported (nested buckets)", label)
		}
		if err := validateOneStorage(env, label, b.Driver, b.Local.Root, b.Lifecycle); err != nil {
			return err
		}
	}
	return nil
}

// validateOneStorage 校验单桶驱动的枚举、环境互斥与生命周期支持情况。
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
	if d == "local" && strings.TrimSpace(localRoot) == "" {
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
