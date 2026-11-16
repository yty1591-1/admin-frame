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

// ApplyJWTSecret 从配置中心密钥子域解析 HS256 签名密钥并写入配置（fail-fast）。
//
// 忽略配置中已有 Secret（禁止 env / 文件明文作为正式路径）；client 与
// Resolve 失败一律返回错误。日志只记录逻辑名与字节长度，不打印明文。
func ApplyJWTSecret(ctx context.Context, cfg *platformconfig.Config, client *secretclient.Client, logger *slog.Logger) error {
	if cfg == nil {
		return fmt.Errorf("secretruntime: config is required")
	}
	if client == nil {
		return fmt.Errorf("secretruntime: secretclient is required for %s", jwtSigningSecretName)
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

// ApplyJWTSecretFromEnv 强制构造 secretclient 并解析 JWT 密钥；任一失败则返回错误。
func ApplyJWTSecretFromEnv(ctx context.Context, cfg *platformconfig.Config, logger *slog.Logger) error {
	client, err := NewFromEnv()
	if err != nil {
		return err
	}
	return ApplyJWTSecret(ctx, cfg, client, logger)
}
