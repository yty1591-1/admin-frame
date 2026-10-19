package storage

import (
	"strings"
	"time"
)

// Config 是 Open 的统一配置（多云可选项）。
type Config struct {
	// Driver：memory | local | s3 | minio | oss | cos | obs
	Driver string

	// KeyPrefix 非空时对返回的 Store 套 WithPrefix（环境级隔离）。
	KeyPrefix string

	// MaxObjectBytes 单对象上限；0 则 DefaultMaxObjectBytes。
	MaxObjectBytes int64

	// PresignPutTTL / PresignGetTTL 预签名默认 TTL（驱动可读取）。
	PresignPutTTL time.Duration
	PresignGetTTL time.Duration

	Local LocalConfig
	S3    S3Config

	// 厂商覆盖；空字段回落 S3。
	OSS OSSConfig
	COS COSConfig
	OBS OBSConfig
}

// S3Config AWS S3 / MinIO / 通用 S3 兼容。
type S3Config struct {
	Endpoint       string `yaml:"endpoint"`
	Region         string `yaml:"region"`
	Bucket         string `yaml:"bucket"`
	AccessKey      string `yaml:"access_key"`
	SecretKey      string `yaml:"secret_key"`
	SessionToken   string `yaml:"session_token"`
	ForcePathStyle bool   `yaml:"force_path_style"`
	// DisableSSL 仅开发；生产禁止。
	DisableSSL bool `yaml:"disable_ssl"`
}

// OSSConfig 阿里云 OSS（默认 L1：S3 兼容）。
type OSSConfig struct {
	Endpoint  string `yaml:"endpoint"`
	Region    string `yaml:"region"`
	Bucket    string `yaml:"bucket"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	// Native 为 true 时走厂商原生 SDK（L2，尚未实现则 Open 失败）。
	Native bool `yaml:"native"`
}

// COSConfig 腾讯云 COS（默认 L1：S3 兼容）。
type COSConfig struct {
	Endpoint  string `yaml:"endpoint"`
	Region    string `yaml:"region"`
	Bucket    string `yaml:"bucket"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Native    bool   `yaml:"native"`
}

// OBSConfig 华为云 OBS（默认 L1：S3 兼容）。
type OBSConfig struct {
	Endpoint  string `yaml:"endpoint"`
	Region    string `yaml:"region"`
	Bucket    string `yaml:"bucket"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Native    bool   `yaml:"native"`
}

func normalizeDriver(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// EffectiveS3 将厂商覆盖合并为 S3 兼容连接参数（L1）。
func (c Config) EffectiveS3() S3Config {
	base := c.S3
	switch normalizeDriver(c.Driver) {
	case "minio":
		base.ForcePathStyle = true
		if base.Region == "" {
			base.Region = "us-east-1"
		}
	case "oss":
		base = mergeCloud(base, c.OSS.Endpoint, c.OSS.Region, c.OSS.Bucket, c.OSS.AccessKey, c.OSS.SecretKey)
	case "cos":
		base = mergeCloud(base, c.COS.Endpoint, c.COS.Region, c.COS.Bucket, c.COS.AccessKey, c.COS.SecretKey)
	case "obs":
		base = mergeCloud(base, c.OBS.Endpoint, c.OBS.Region, c.OBS.Bucket, c.OBS.AccessKey, c.OBS.SecretKey)
	}
	return base
}

func mergeCloud(base S3Config, endpoint, region, bucket, ak, sk string) S3Config {
	if endpoint != "" {
		base.Endpoint = endpoint
	}
	if region != "" {
		base.Region = region
	}
	if bucket != "" {
		base.Bucket = bucket
	}
	if ak != "" {
		base.AccessKey = ak
	}
	if sk != "" {
		base.SecretKey = sk
	}
	return base
}
