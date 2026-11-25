package secretruntime

import (
	"context"
	"fmt"
	"strings"

	"github.com/admin-frame/admin-frame/pkg/secretclient"
)

const (
	// RedisPasswordSecretName 是 Redis 口令的配置中心密钥名（platform 域）。
	RedisPasswordSecretName = "redis.password"
	// RedisPasswordValueKey 是注入 appconfig Values 的扁平路径。
	RedisPasswordValueKey = "redis.password"
	// AsyncBrokerURLSecretName 是 NATS URL 的配置中心密钥名（platform 域）。
	AsyncBrokerURLSecretName = "async.broker.url"
	// AsyncBrokerURLValueKey 是注入 appconfig Values 的扁平路径。
	AsyncBrokerURLValueKey = "async.broker.url"

	infraPlatformNamespace = "platform"
)

// ResolveRedisPassword 从密钥子域解析 Redis 口令；缺失时返回空串（无密 Redis 合法）。
func ResolveRedisPassword(ctx context.Context, env string, client *secretclient.Client) (string, error) {
	return resolveOptionalPlatformSecret(ctx, env, client, RedisPasswordSecretName)
}

// ResolveRedisPasswordFromEnv 构造 secretclient 并解析 Redis 口令。
func ResolveRedisPasswordFromEnv(ctx context.Context, env string) (string, error) {
	client, err := NewFromEnv()
	if err != nil {
		return "", err
	}
	return ResolveRedisPassword(ctx, env, client)
}

// ResolveAsyncBrokerURL 从密钥子域解析 NATS URL；缺失时返回空串。
func ResolveAsyncBrokerURL(ctx context.Context, env string, client *secretclient.Client) (string, error) {
	return resolveOptionalPlatformSecret(ctx, env, client, AsyncBrokerURLSecretName)
}

// ResolveAsyncBrokerURLFromEnv 构造 secretclient 并解析 NATS URL。
func ResolveAsyncBrokerURLFromEnv(ctx context.Context, env string) (string, error) {
	client, err := NewFromEnv()
	if err != nil {
		return "", err
	}
	return ResolveAsyncBrokerURL(ctx, env, client)
}

func resolveOptionalPlatformSecret(ctx context.Context, env string,
	client *secretclient.Client, name string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("secretruntime: secretclient is required for %s", name)
	}
	resolvedEnv := strings.TrimSpace(env)
	if resolvedEnv == "" {
		return "", fmt.Errorf("secretruntime: env is required to resolve %s", name)
	}
	value, err := client.Resolve(ctx, resolvedEnv, infraPlatformNamespace, name)
	if err != nil {
		// 可选密钥：未创建时返回空，由条件探测 / 装配决定是否失败。
		return "", nil
	}
	return strings.TrimSpace(value), nil
}
