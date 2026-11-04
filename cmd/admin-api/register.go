package main

import (
	"net/http"

	"github.com/admin-frame/admin-frame/cmd/internal/auditruntime"
	identityruntime "github.com/admin-frame/admin-frame/cmd/internal/identityruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/notificationruntime"
	tenantruntime "github.com/admin-frame/admin-frame/cmd/internal/tenantruntime"
	auditadmin "github.com/admin-frame/admin-frame/internal/audit/transport/admin"
	iamadmin "github.com/admin-frame/admin-frame/internal/iam/transport/admin"
	identityadmin "github.com/admin-frame/admin-frame/internal/identity/transport/admin"
	notificationadmin "github.com/admin-frame/admin-frame/internal/notification/transport/admin"
	notificationrest "github.com/admin-frame/admin-frame/internal/notification/transport/rest"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	tenantadmin "github.com/admin-frame/admin-frame/internal/tenant/transport/admin"
)

// registerRoutes 装配 A 端依赖并注册本端路由。
//
// rt 由 NewAuthorizer 阶段构造并复用：IAM 管理端与鉴权中间件必须看到同一份
// 角色 / 绑定数据，否则本地内存模式下会出现「刚授权的角色不生效」。
// identityRT 用于注册身份域（登录 / 刷新 / 登出 / 会话管理）路由。
func registerRoutes(deps *platform.Deps, mux *http.ServeMux, rt *iamRuntime, identityRT *identityruntime.Runtime) error {
	if rt != nil && rt.Handler != nil {
		iamadmin.Register(mux, rt.Handler, deps.AuthMW, deps.RequirePermission)
	}
	if identityRT != nil && identityRT.Handler != nil {
		identityadmin.Register(mux, identityRT.Handler, deps.AuthMW, deps.RequirePermission)
	}
	auditRT, err := auditruntime.New(deps)
	if err != nil {
		return err
	}
	if auditRT != nil && auditRT.Handler != nil {
		auditadmin.Register(mux, auditRT.Handler, deps.AuthMW, deps.RequirePermission)
	}
	tenantRT, err := tenantruntime.New(deps, tenantSessionRevoker(identityRT))
	if err != nil {
		return err
	}
	if tenantRT != nil && tenantRT.Handler != nil {
		tenantadmin.Register(mux, tenantRT.Handler, deps.AuthMW, deps.RequirePermission)
	}
	// ENT-10：通知管理端（查询 / 代发 / DLQ 补偿 / 模板管理）+ 邀请邮件接入。
	// A 端只需要轻量出口（编排消费在 worker）；async 未启用（单进程最小形态）
	// 时整体跳过，租户邀请保持「只落库不发送」阶段语义。
	services, err := notificationruntime.NewAdminServices(deps)
	if err != nil {
		return err
	}
	if services != nil {
		if tenantRT != nil {
			tenantRT.SetInvitationNotifier(notificationruntime.NewInvitationNotifier(services.Dispatcher, ""))
		}
		notificationHandler, herr := notificationrest.NewHandler(notificationrest.HandlerConfig{
			Queries:   services.Queries,
			Templates: services.Templates,
			Replay:    services.Replay,
			Send:      services.Send,
			Channel:   "admin",
		})
		if herr != nil {
			return herr
		}
		notificationadmin.Register(mux, notificationHandler, deps.AuthMW, deps.RequirePermission)
	}
	return nil
}

// tenantSessionRevoker 提取 identity 的租户会话撤销器；身份域未启用时返回 nil。
//
// nil 时租户停用只改状态不撤令牌——仅允许出现在 identity.enabled=false 的
// 部署里；正常 A 端装配两者同时启用，撤销链路必须闭合（D-02=①）。
func tenantSessionRevoker(identityRT *identityruntime.Runtime) app.SessionRevoker {
	if identityRT == nil {
		return nil
	}
	return identityRT.TenantRevoker
}
