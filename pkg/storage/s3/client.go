package s3

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

// Open 根据统一 Config 构造 S3 兼容 Store（同时实现 Presigner）。
func Open(ctx context.Context, cfg storage.Config) (storage.Store, error) {
	driver := cfg.Driver
	if driver == "" {
		driver = "s3"
	}
	if err := storage.ValidateCloudL1(driver, cfg); err != nil {
		return nil, err
	}
	sc := cfg.EffectiveS3()
	if sc.Region == "" {
		sc.Region = "us-east-1"
	}
	client, presign, err := newClients(ctx, sc)
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
		presign:  presign,
		bucket:   sc.Bucket,
		maxBytes: maxBytes,
		putTTL:   putTTL,
		getTTL:   getTTL,
	}, nil
}

func newClients(ctx context.Context, sc storage.S3Config) (*s3.Client, *s3.PresignClient, error) {
	var opts []func(*awsconfig.LoadOptions) error
	opts = append(opts, awsconfig.WithRegion(sc.Region))
	if sc.AccessKey != "" || sc.SecretKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(sc.AccessKey, sc.SecretKey, sc.SessionToken),
		))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: load aws config: %v", storage.ErrInvalidConfig, err)
	}

	ep := normalizeS3Endpoint(sc)
	usePathStyle := sc.ForcePathStyle || endpointNeedsPathStyle(ep)
	s3opts := []func(*s3.Options){
		func(o *s3.Options) {
			o.UsePathStyle = usePathStyle
		},
	}
	if ep != "" {
		s3opts = append(s3opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(ep)
		})
	}
	client := s3.NewFromConfig(awsCfg, s3opts...)
	return client, s3.NewPresignClient(client), nil
}

// normalizeS3Endpoint 为裸主机补齐 scheme，空端点保持空串。
func normalizeS3Endpoint(sc storage.S3Config) string {
	ep := strings.TrimSpace(sc.Endpoint)
	if ep == "" {
		return ""
	}
	if strings.HasPrefix(ep, "http://") || strings.HasPrefix(ep, "https://") {
		return ep
	}
	if sc.DisableSSL {
		return "http://" + ep
	}
	return "https://" + ep
}

// endpointNeedsPathStyle 在自定义端点为 localhost / 字面量 IP 时强制 path-style。
//
// 虚拟主机风格会变成 bucket.127.0.0.1，本地 S3 兼容服务（RustFS/MinIO）无法解析。
func endpointNeedsPathStyle(endpoint string) bool {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return false
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "" {
		host = endpoint
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	return net.ParseIP(host) != nil
}
