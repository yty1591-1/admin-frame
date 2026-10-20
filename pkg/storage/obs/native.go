package obs

import (
	"context"
	"sync/atomic"
	"time"

	hwobs "github.com/huaweicloud/huaweicloud-sdk-go-obs/obs"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

// Store 华为云 OBS 原生 SDK（L2）实现。
type Store struct {
	client   *hwobs.ObsClient
	bucket   string
	maxBytes int64
	putTTL   time.Duration
	getTTL   time.Duration
	closed   atomic.Bool
}

func openNative(_ context.Context, cfg storage.Config) (storage.Store, error) {
	if err := storage.ValidateCloudL2("obs", cfg); err != nil {
		return nil, err
	}
	p := cfg.NativeParams("obs")
	client, err := hwobs.New(p.AccessKey, p.SecretKey, ensureHTTPEndpoint(p.Endpoint))
	if err != nil {
		return nil, err
	}

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
