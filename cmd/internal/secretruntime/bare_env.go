package secretruntime

import (
	"fmt"
	"strings"

	"github.com/admin-frame/admin-frame/internal/shared/deployenv"
)

// RejectBareSecretEnv 仅允许 local/dev/test 经裸环境变量注入业务密钥（CC-R-03）。
//
// 空 env、staging、prod 一律拒绝：空值不再视为「非 prod-like」而放行。
func RejectBareSecretEnv(env, secretName string) error {
	if deployenv.IsLocalDev(env) {
		return nil
	}
	name := strings.TrimSpace(secretName)
	if name == "" {
		name = "business secret"
	}
	shown := strings.TrimSpace(env)
	if shown == "" {
		shown = "<empty>"
	}
	return fmt.Errorf(
		"secretruntime: bare env secret %q is forbidden when env=%s; resolve via secretclient/config-api (CC-R-03)",
		name, shown,
	)
}
