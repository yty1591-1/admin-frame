package main

import (
	"net/http"

	identityruntime "github.com/admin-frame/admin-frame/cmd/internal/identityruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/notificationruntime"
	identityc "github.com/admin-frame/admin-frame/internal/identity/transport/c"
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
	// 通知回执回调：验签密钥未配置（fail closed，构造失败）时该部署形态
	// 无回执能力，投递以 accepted 为终态；不挂无验签的公开写端点。
	services, err := notificationruntime.NewAdminServices(deps)
	if err != nil {
		return err
	}
	if services != nil {
		callbackHandler, cerr := notificationcallback.NewHandler(services.Receipts)
		if cerr != nil {
			return cerr
		}
		mux.Handle("POST /v1/notification-callbacks/receipt", http.HandlerFunc(callbackHandler.Receipt))
	}
	return nil
}
