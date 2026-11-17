package secretruntime

import (
	"fmt"
	"strings"

	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
)

// RejectBareSecretEnv 在 staging/prod 拒绝业务密钥走裸环境变量（CC-R-03）。
//
// Wave 0：先禁止静默/直读成功；完整迁入 secretclient 见 CC-R-05。
// local/dev/test 仍允许装配期 env 注入以便本机冒烟。
func RejectBareSecretEnv(env, secretName string) error {
	if !platformconfig.IsProdLikeEnv(env) {
		return nil
	}
	name := strings.TrimSpace(secretName)
	if name == "" {
		name = "business secret"
	}
	return fmt.Errorf(
		"secretruntime: bare env secret %q is forbidden when env=%s; resolve via secretclient/config-api (CC-R-03)",
		name, strings.TrimSpace(env),
	)
}
