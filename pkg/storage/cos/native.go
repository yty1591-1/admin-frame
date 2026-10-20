package cos

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	tencentcos "github.com/tencentyun/cos-go-sdk-v5"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

// Store 腾讯云 COS 原生 SDK（L2）实现。
type Store struct {
	client   *tencentcos.Client
	ak       string
	sk       string
	bucket   string
	maxBytes int64
	putTTL   time.Duration
	getTTL   time.Duration
	closed   atomic.Bool
}

func openNative(_ context.Context, cfg storage.Config) (storage.Store, error) {
	if err := storage.ValidateCloudL2("cos", cfg); err != nil {
		return nil, err
	}
	p := cfg.NativeParams("cos")
	bucketURL, err := buildBucketURL(p.Bucket, p.Region, p.Endpoint)
	if err != nil {
		return nil, err
	}
	client := tencentcos.NewClient(&tencentcos.BaseURL{BucketURL: bucketURL}, &http.Client{
		Transport: &tencentcos.AuthorizationTransport{
			SecretID:  p.AccessKey,
			SecretKey: p.SecretKey,
		},
	})

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
		ak:       p.AccessKey,
		sk:       p.SecretKey,
		bucket:   p.Bucket,
		maxBytes: maxBytes,
		putTTL:   putTTL,
		getTTL:   getTTL,
	}, nil
}

func buildBucketURL(bucket, region, endpoint string) (*url.URL, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = fmt.Sprintf("cos.%s.myqcloud.com", region)
	}
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")
	// 已是 bucket.cos.region... 形式则直接用
	if strings.HasPrefix(endpoint, bucket+".") {
		return url.Parse("https://" + endpoint)
	}
	return url.Parse(fmt.Sprintf("https://%s.%s", bucket, endpoint))
}

func (s *Store) ready(ctx context.Context) error {
	if s == nil || s.closed.Load() || s.client == nil {
		return storage.ErrClosed
	}
	return ctx.Err()
}
