package main

import (
	"context"
	"fmt"
	"net/http"

	identityruntime "github.com/admin-frame/admin-frame/cmd/internal/identityruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/notificationruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/secretruntime"
	identityc "github.com/admin-frame/admin-frame/internal/identity/transport/c"
	identityrest "github.com/admin-frame/admin-frame/internal/identity/transport/rest"
	notificationcallback "github.com/admin-frame/admin-frame/internal/notification/transport/callback"
	"github.com/admin-frame/admin-frame/internal/platform"
)

// registerRoutes 装配 C 端业务依赖并注册本端路由。
//
// C 端承载身份认证与平台 probe；通知回执回调（ENT-10 NT-07）也挂在本端——
// 它是供应商服务器到服务器的公网入口，验签独立于登录鉴权（三件套见
// transport/callback 包注释）。新增业务资源时需同时补权限点、OpenAPI 契约
// 与 TEST-03 用户归属用例。
func registerRoutes(deps *platform.Deps, mux *http.ServeMux, identityRT *identityruntime.Runtime) error {
	if identityRT != nil && identityRT.CHandler != nil {
		identityc.Register(mux, identityRT.CHandler, deps.AuthMW)
	}
	// M2M 机器令牌：C 端认证路由由 identityc 承载，但签发端点与 A/B 端共用
	// rest.Handler 实现，这里显式注册以保持三端契约一致（§21.3）。
	// 路径写字面量：契约对账（tests/contract）只识别 mux.Handle 的字面量形态。
	if identityRT != nil && identityRT.Handler != nil && identityRT.Handler.M2MEnabled() {
		mux.Handle("POST /c/v1/auth/token", identityrest.M2MTokenHandler(identityRT.Handler))
	}
	// 通知回执回调：验签密钥未配置（fail closed，构造失败）时该部署形态
	// 无回执能力，投递以 accepted 为终态；不挂无验签的公开写端点。
	services, err := notificationruntime.NewAdminServices(deps)
	if err != nil {
		return err
	}
	if services != nil {
		secret, serr := resolveNotificationCallbackSecret(deps)
		if serr != nil {
			return serr
		}
		callbackHandler, cerr := notificationcallback.NewHandler(services.Receipts, secret)
		if cerr != nil {
			return cerr
		}
		mux.Handle("POST /v1/notification-callbacks/receipt", http.HandlerFunc(callbackHandler.Receipt))
	}
	return nil
}

// resolveNotificationCallbackSecret 优先 secretclient，local/dev/test 可回退裸 env。
func resolveNotificationCallbackSecret(deps *platform.Deps) (string, error) {
	env := ""
	if deps != nil && deps.Config != nil {
		env = deps.Config.Env
	}
	secret, err := secretruntime.ResolveNotificationCallbackSecretFromEnv(context.Background(), env)
	if err == nil {
		return secret, nil
	}
	fallback, ferr := notificationcallback.LoadCallbackSecretFromEnv(env)
	if ferr != nil {
		return "", fmt.Errorf("notification callback secret: resolve failed (%v); bare env: %w", err, ferr)
	}
	return fallback, nil
}
