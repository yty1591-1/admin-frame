package s3

import (
	"context"
	"fmt"
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

	s3opts := []func(*s3.Options){
		func(o *s3.Options) {
			o.UsePathStyle = sc.ForcePathStyle
		},
	}
	if sc.Endpoint != "" {
		ep := sc.Endpoint
		if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
			if sc.DisableSSL {
				ep = "http://" + ep
			} else {
				ep = "https://" + ep
			}
		}
		s3opts = append(s3opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(ep)
		})
	}
	client := s3.NewFromConfig(awsCfg, s3opts...)
	return client, s3.NewPresignClient(client), nil
}
