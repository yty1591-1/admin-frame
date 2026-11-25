package secretruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/admin-frame/admin-frame/pkg/secretclient"
)

const demoHMACSecretName = "platform.demo.hmac_key"

// WarmDemoHMAC 解析演示 HMAC 密钥并仅记录指纹（AC-CC-16：日志无明文）。
//
// 证明 biz-api → config-api 真实消费路径；失败不阻断启动。
func WarmDemoHMAC(ctx context.Context, client *secretclient.Client, env string, logger *slog.Logger) error {
	if client == nil {
		return nil
	}
	if env == "" {
		env = "dev"
	}
	value, err := client.Resolve(ctx, env, "platform", demoHMACSecretName)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", demoHMACSecretName, err)
	}
	sum := sha256.Sum256([]byte(value))
	fp := hex.EncodeToString(sum[:8])
	if logger != nil {
		logger.Info("demo hmac secret resolved",
			"name", demoHMACSecretName, "fingerprint", fp, "bytes", len(value))
	}
	return nil
}
