package secretruntime

import (
	"context"
	"fmt"
	"strings"

	"github.com/admin-frame/admin-frame/pkg/secretclient"
)

const (
	// StorageS3AccessKeySecretName 是对象存储 AccessKey 的密钥名（platform 域）。
	StorageS3AccessKeySecretName = "storage.s3.access_key" //nolint:gosec // G101: 密钥名，非凭据值
	// StorageS3SecretKeySecretName 是对象存储 SecretKey 的密钥名（platform 域）。
	StorageS3SecretKeySecretName = "storage.s3.secret_key" //nolint:gosec // G101: 密钥名，非凭据值
	// StorageS3AccessKeyValueKey 是注入 appconfig Values 的扁平路径。
	StorageS3AccessKeyValueKey = "storage.s3.access_key"
	// StorageS3SecretKeyValueKey 是注入 appconfig Values 的扁平路径。
	StorageS3SecretKeyValueKey = "storage.s3.secret_key" //nolint:gosec // G101: Values 扁平路径名，非凭据值
	platformNamespace          = "platform"
)

// ResolveStorageS3Credentials 从密钥子域解析对象存储 AK/SK（fail-fast）。
//
// 与 JWT 等同属「只来自 secretclient」的正式路径：调用方在
// appconfig.LoadFromValues 之前把结果写入 Values，禁止环境变量明文。
// 非 s3/minio 驱动或 keys 为空时不调用；错误只含逻辑名（SEC-05）。
func ResolveStorageS3Credentials(ctx context.Context, env string,
	client *secretclient.Client) (accessKey, secretKey string, err error) {
	if client == nil {
		return "", "", fmt.Errorf("secretruntime: secretclient is required for storage credentials")
	}
	resolvedEnv := strings.TrimSpace(env)
	if resolvedEnv == "" {
		resolvedEnv = "dev"
	}
	accessKey, err = resolveStorageSecret(ctx, resolvedEnv, client, StorageS3AccessKeySecretName)
	if err != nil {
		return "", "", err
	}
	secretKey, err = resolveStorageSecret(ctx, resolvedEnv, client, StorageS3SecretKeySecretName)
	if err != nil {
		return "", "", err
	}
	return accessKey, secretKey, nil
}

// ResolveStorageS3CredentialsFromEnv 构造 secretclient 并解析对象存储 AK/SK。
func ResolveStorageS3CredentialsFromEnv(ctx context.Context, env string) (accessKey, secretKey string, err error) {
	client, err := NewFromEnv()
	if err != nil {
		return "", "", err
	}
	return ResolveStorageS3Credentials(ctx, env, client)
}

func resolveStorageSecret(ctx context.Context, env string, client *secretclient.Client, name string) (string, error) {
	value, err := client.Resolve(ctx, env, platformNamespace, name)
	if err != nil {
		return "", fmt.Errorf("secretruntime: resolve %s: %w", name, err)
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("secretruntime: %s resolved empty", name)
	}
	return value, nil
}
