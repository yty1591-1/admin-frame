// Package main 是 admin-api 进程入口（管理端通道）。
package main

import (
	iamruntime "github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	"github.com/admin-frame/admin-frame/internal/iam"
	iamconfig "github.com/admin-frame/admin-frame/internal/iam/config"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/pkg/rbac"
)

// iamRuntime 是共享装配结果；保留本端别名让 main / register 的签名保持稳定。
type iamRuntime = iamruntime.Runtime

// newIAMRuntime 装配 A 端 IAM，并注入 A 端静态回滚表。
func newIAMRuntime(deps *platform.Deps, iamCfg iamconfig.IAMConfig) (*iamRuntime, error) {
	return iamruntime.New(deps, iamCfg, iamruntime.Options{StaticChecker: staticChecker()})
}

// staticChecker 是 mode=off 的回滚表：admin 为超级角色，ops 持有平台只读权限。
//
// 这是装配期的临时真相，不得在 enforce 模式下作为兜底；enforce 加载失败必须 503。
func staticChecker() *rbac.Checker {
	checker := rbac.New(rbac.WithSuperRoles("admin"))
	checker.GrantRole("ops",
		rbac.Permission(iam.PermPermissionRead),
		rbac.Permission(iam.PermRoleRead),
		rbac.Permission(iam.PermBindingRead),
		rbac.Permission(iam.PermChangeRead),
		rbac.Permission(iam.PermAuditRead),
		rbac.Permission(iam.PermConfigRead),
	)
	return checker
}
