package testkit

import (
	"context"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/storage"
	// 注册 S3 / MinIO 存储驱动（S-06：空白导入仅用于驱动注册）。
	_ "github.com/admin-frame/admin-frame/pkg/storage/s3"
)

// Storage 打开 MinIO（S3 兼容）测试存储；未配置 TEST_S3_* 时跳过用例。
//
// 测试基座统一使用 path-style 与测试 bucket；http 端点自动关闭 TLS，
// https 端点保持 TLS 校验，不提供跳过证书校验的开关。
func Storage(t *testing.T) storage.Store {
	t.Helper()
	endpoint := RequireEnv(t, EnvS3Endpoint)
	accessKey := RequireEnv(t, EnvS3AccessKey)
	secretKey := RequireEnv(t, EnvS3SecretKey)
	bucket := RequireEnv(t, EnvS3Bucket)

	store, err := storage.Open(context.Background(), storage.Config{
		Driver: "minio",
		S3: storage.S3Config{
			Endpoint:       endpoint,
			Region:         "us-east-1",
			Bucket:         bucket,
			AccessKey:      accessKey,
			SecretKey:      secretKey,
			ForcePathStyle: true,
			DisableSSL:     strings.HasPrefix(endpoint, "http://"),
		},
		KeyPrefix: "testkit/",
	})
	if err != nil {
		t.Fatalf("open test storage: %v", err)
	}
	t.Cleanup(func() {
		// 关闭失败仅影响客户端资源回收，不覆盖测试断言结果。
		_ = store.Close()
	})
	return store
}
