// Package secretruntime 装配业务进程侧密钥解析（M2 / M3）。
//
// 业务密钥（含 JWT）仅经 secretclient → config-api；CONFIG_API_URL 与
// CONFIG_SERVICE_IDENTITY 必填，缺失或解析失败 fail-fast。
package secretruntime

import (
	"fmt"
	"os"
	"strings"

	"github.com/admin-frame/admin-frame/pkg/secretclient"
)

// NewFromEnv 构造 secretclient；引导环境变量缺失或不完整时返回错误。
func NewFromEnv() (*secretclient.Client, error) {
	base := strings.TrimSpace(os.Getenv("CONFIG_API_URL"))
	identity := strings.TrimSpace(os.Getenv("CONFIG_SERVICE_IDENTITY"))
	if base == "" || identity == "" {
		return nil, fmt.Errorf("secretruntime: CONFIG_API_URL and CONFIG_SERVICE_IDENTITY are required")
	}
	return secretclient.New(secretclient.Config{
		BaseURL: base, ServiceIdentity: identity,
	})
}
