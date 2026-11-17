package rest

import (
	"os"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/deployenv"
)

// CONFIG_API_NAMESPACE_ALLOWLIST 格式：
//
//	admin-api:platform,identity;biz-api:platform,identity,iam;*:platform
//
// 服务身份取 X-Service-Identity / 证书 SAN 的末段（/ 之后）或全文精确匹配。
// `*` 表示任意服务；namespace 侧 `*` 表示任意 namespace。
//
// staging/prod：未配置 allowlist 时拒绝拉取（CC-R-04）。
// local/dev/test：未配置则放行（本机冒烟）。
const namespaceAllowlistEnv = "CONFIG_API_NAMESPACE_ALLOWLIST"

var (
	allowlistOnce sync.Once
	allowlistMap  map[string]map[string]struct{}
	allowlistErr  error
)

func resetNamespaceAllowlistForTest() {
	allowlistOnce = sync.Once{}
	allowlistMap = nil
	allowlistErr = nil
}

func loadNamespaceAllowlist() (map[string]map[string]struct{}, error) {
	allowlistOnce.Do(func() {
		raw := strings.TrimSpace(os.Getenv(namespaceAllowlistEnv))
		if raw == "" {
			allowlistMap = nil
			return
		}
		out := make(map[string]map[string]struct{})
		for _, entry := range strings.Split(raw, ";") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			parts := strings.SplitN(entry, ":", 2)
			if len(parts) != 2 {
				allowlistErr = apperr.Internal("invalid "+namespaceAllowlistEnv+": "+entry, nil)
				return
			}
			svc := strings.TrimSpace(parts[0])
			nsSet := make(map[string]struct{})
			for _, ns := range strings.Split(parts[1], ",") {
				ns = strings.TrimSpace(ns)
				if ns == "" {
					continue
				}
				nsSet[ns] = struct{}{}
			}
			if svc == "" || len(nsSet) == 0 {
				allowlistErr = apperr.Internal("invalid "+namespaceAllowlistEnv+": "+entry, nil)
				return
			}
			out[svc] = nsSet
		}
		allowlistMap = out
	})
	return allowlistMap, allowlistErr
}

// authorizeNamespace 校验服务身份是否可拉取指定 namespace（CC-R-04）。
//
// deployEnv 必须是进程部署环境（CONFIGCENTER_ENV / deps.Config.Env），
// 不得用请求 path/query 里的数据 env，否则 prod 部署可用 env=dev 绕过空 allowlist。
func authorizeNamespace(deployEnv, serviceIdentity, namespace string) error {
	identity := strings.TrimSpace(serviceIdentity)
	ns := strings.TrimSpace(namespace)
	if identity == "" {
		return apperr.Forbidden("service identity required")
	}
	if ns == "" {
		return apperr.BadRequest("namespace required")
	}
	list, err := loadNamespaceAllowlist()
	if err != nil {
		return err
	}
	if list == nil {
		// 仅 local/dev/test 允许空 allowlist；空部署 env 也拒绝。
		if !deployenv.IsLocalDev(deployEnv) {
			shown := strings.TrimSpace(deployEnv)
			if shown == "" {
				shown = "<empty>"
			}
			return apperr.Forbidden(namespaceAllowlistEnv + " required when deploy env=" + shown)
		}
		return nil
	}
	candidates := []string{identity, identityLeaf(identity), "*"}
	for _, key := range candidates {
		allowed, ok := list[key]
		if !ok {
			continue
		}
		if _, ok := allowed["*"]; ok {
			return nil
		}
		if _, ok := allowed[ns]; ok {
			return nil
		}
	}
	return apperr.Forbidden("service " + identity + " is not allowed to read namespace " + ns)
}

func identityLeaf(identity string) string {
	if i := strings.LastIndex(identity, "/"); i >= 0 && i+1 < len(identity) {
		return identity[i+1:]
	}
	return identity
}
