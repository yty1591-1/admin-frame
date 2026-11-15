package secretruntime

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/pkg/secretclient"
)

const jwtSigningSecretName = "auth.jwt.private_key" //nolint:gosec // G101: 配置中心密钥名，非凭据值。

// ApplyJWTSecret 在 auth.jwt.secret 为空时，从配置中心密钥子域补齐 HS256 签名密钥（Q9 / M3）。
//
// 优先保留 ENT-07 Provider / 环境变量已注入的值；仅在仍为空且 secretclient 可用时回源。
// 失败返回错误，由调用方决定是否 fail-fast（jwt 模式必填）。
func ApplyJWTSecret(ctx context.Context, cfg *platformconfig.Config, client *secretclient.Client, logger *slog.Logger) error {
	if cfg == nil {
		return nil
	}
	if strings.TrimSpace(cfg.Auth.JWT.Secret) != "" {
		return nil
	}
	if client == nil {
		return nil
	}
	env := strings.TrimSpace(cfg.Env)
	if env == "" {
		env = "dev"
	}
	value, err := client.Resolve(ctx, env, "platform", jwtSigningSecretName)
	if err != nil {
		return fmt.Errorf("secretruntime: resolve %s: %w", jwtSigningSecretName, err)
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("secretruntime: %s resolved empty", jwtSigningSecretName)
	}
	cfg.Auth.JWT.Secret = value
	if logger != nil {
		logger.Info("jwt signing secret applied from config center",
			"name", jwtSigningSecretName, "bytes", len(value))
	}
	return nil
}

// ApplyJWTSecretFromEnv 构造可选 secretclient 并尝试补齐 JWT 密钥；未配置客户端时返回 nil。
func ApplyJWTSecretFromEnv(ctx context.Context, cfg *platformconfig.Config, logger *slog.Logger) error {
	client, err := NewFromEnv()
	if err != nil {
		return err
	}
	if client == nil {
		return nil
	}
	return ApplyJWTSecret(ctx, cfg, client, logger)
}
