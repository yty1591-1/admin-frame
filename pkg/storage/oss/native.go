package oss

import (
	"context"
	"sync/atomic"
	"time"

	alioss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

// Store 阿里云 OSS 原生 SDK（L2）实现。
type Store struct {
	client   *alioss.Client
	bucket   string
	maxBytes int64
	putTTL   time.Duration
	getTTL   time.Duration
	closed   atomic.Bool
}

func openNative(_ context.Context, cfg storage.Config) (storage.Store, error) {
	if err := storage.ValidateCloudL2("oss", cfg); err != nil {
		return nil, err
	}
	p := cfg.NativeParams("oss")
	builder := alioss.LoadDefaultConfig().
		WithCredentialsProvider(credentials.NewStaticCredentialsProvider(p.AccessKey, p.SecretKey)).
		WithRegion(p.Region)
	if p.Endpoint != "" {
		builder = builder.WithEndpoint(p.Endpoint)
	}
	client := alioss.NewClient(builder)

	maxBytes := cfg.MaxObjectBytes
	if maxBytes == 0 {
		maxBytes = storage.DefaultMaxObjectBytes
	}
	putTTL := cfg.PresignPutTTL
	if putTTL <= 0 {
		putTTL = 15 * time.Minute
	}
	getTTL := cfg.PresignGetTTL
	if getTTL <= 0 {
		getTTL = 15 * time.Minute
	}
	return &Store{
		client:   client,
		bucket:   p.Bucket,
		maxBytes: maxBytes,
		putTTL:   putTTL,
		getTTL:   getTTL,
	}, nil
}

func (s *Store) ready(ctx context.Context) error {
	if s == nil || s.closed.Load() || s.client == nil {
		return storage.ErrClosed
	}
	return ctx.Err()
}
