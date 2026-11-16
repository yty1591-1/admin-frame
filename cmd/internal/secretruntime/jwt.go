package secretruntime

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/pkg/secretclient"
)

const (
	jwtSigningSecretName = "auth.jwt.private_key" //nolint:gosec // G101: 配置中心密钥名，非凭据值。
	// JWTSecretValueKey 是注入 appconfig Values 的扁平路径（与 yaml 字段对齐）。
	JWTSecretValueKey = "auth.jwt.secret"
)

// ResolveJWTSecret 从配置中心密钥子域解析 HS256 签名密钥明文（fail-fast）。
//
// 正式启动路径应在 appconfig.LoadFromValues / Validate 之前调用，把返回值写入
// Values[JWTSecretValueKey]，避免源码占位密钥。日志只记录逻辑名与字节长度。
func ResolveJWTSecret(ctx context.Context, env string, client *secretclient.Client) (string, error) {
	if client == nil {
		return "", fmt.Errorf("secretruntime: secretclient is required for %s", jwtSigningSecretName)
	}
	resolvedEnv := strings.TrimSpace(env)
	if resolvedEnv == "" {
		resolvedEnv = "dev"
	}
	value, err := client.Resolve(ctx, resolvedEnv, "platform", jwtSigningSecretName)
	if err != nil {
		return "", fmt.Errorf("secretruntime: resolve %s: %w", jwtSigningSecretName, err)
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("secretruntime: %s resolved empty", jwtSigningSecretName)
	}
	return value, nil
}

// ResolveJWTSecretFromEnv 强制构造 secretclient 并解析 JWT 密钥；任一失败则返回错误。
func ResolveJWTSecretFromEnv(ctx context.Context, env string) (string, error) {
	client, err := NewFromEnv()
	if err != nil {
		return "", err
	}
	return ResolveJWTSecret(ctx, env, client)
}

// ApplyJWTSecret 解析 JWT 密钥并写入配置（fail-fast）。
//
// 忽略配置中已有 Secret（禁止 env / 文件明文作为正式路径）。优先在 Load
// 前使用 ResolveJWTSecret；本函数保留给已构造 Config 后的补齐与单测。
func ApplyJWTSecret(ctx context.Context, cfg *platformconfig.Config, client *secretclient.Client, logger *slog.Logger) error {
	if cfg == nil {
		return fmt.Errorf("secretruntime: config is required")
	}
	value, err := ResolveJWTSecret(ctx, cfg.Env, client)
	if err != nil {
		return err
	}
	cfg.Auth.JWT.Secret = value
	if logger != nil {
		logger.Info("jwt signing secret applied from config center",
			"name", jwtSigningSecretName, "bytes", len(value))
	}
	return nil
}

// ApplyJWTSecretFromEnv 强制构造 secretclient 并解析 JWT 密钥写入配置。
func ApplyJWTSecretFromEnv(ctx context.Context, cfg *platformconfig.Config, logger *slog.Logger) error {
	client, err := NewFromEnv()
	if err != nil {
		return err
	}
	return ApplyJWTSecret(ctx, cfg, client, logger)
}
