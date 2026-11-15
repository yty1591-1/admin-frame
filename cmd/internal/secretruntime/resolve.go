// Package secretruntime 装配业务进程侧密钥解析（M2）。
//
// 当 CONFIG_API_URL 与 CONFIG_SERVICE_IDENTITY 均设置时返回可用客户端；
// 否则返回 nil（进程继续使用环境变量 / 文件凭据，直至 M3 删除样例）。
package secretruntime

import (
	"fmt"
	"os"
	"strings"

	"github.com/admin-frame/admin-frame/pkg/secretclient"
)

// NewFromEnv 构造可选 secretclient；未配置时返回 (nil, nil)。
func NewFromEnv() (*secretclient.Client, error) {
	base := strings.TrimSpace(os.Getenv("CONFIG_API_URL"))
	identity := strings.TrimSpace(os.Getenv("CONFIG_SERVICE_IDENTITY"))
	if base == "" && identity == "" {
		return nil, nil
	}
	if base == "" || identity == "" {
		return nil, fmt.Errorf("secretruntime: CONFIG_API_URL and CONFIG_SERVICE_IDENTITY must both be set")
	}
	return secretclient.New(secretclient.Config{
		BaseURL: base, ServiceIdentity: identity,
	})
}
