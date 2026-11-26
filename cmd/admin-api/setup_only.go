package main

import (
	"context"
	"os"
	"strings"

	"github.com/admin-frame/admin-frame/cmd/internal/configboot"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
)

// runSetupOnly 在缺少必填配置键时以最小 HTTP 面启动 admin-api（ADR-0018 决策 2）。
//
// 只挂 /healthz、/readyz、/version 与安装向导代理：前端（外部 nginx 托管的
// SPA）据此完成首装或 repair，写操作仍需 X-Config-Install-Token；安装完成
// 后进程默认自动退出（ADMIN_FRAME_SETUP_ONLY_AUTO_EXIT=false 可关闭），由
// 编排重启为正常进程。其他进程不做降级，保持 fail-fast。
func runSetupOnly(ctx context.Context, boot *configboot.Result, gaps []string, build httpserver.BuildInfo) error {
	addr := strings.TrimSpace(os.Getenv("ADMIN_API_ADDR"))
	if addr == "" {
		addr = ":8081"
	}
	proxy := configboot.NewInstallProxy(boot.ConfigAPIURL, boot.HTTPClient)
	return platform.RunSetupOnly(ctx, platform.SetupOnlyOptions{
		Service:     "admin-api",
		Channel:     "admin",
		Env:         boot.Env,
		Addr:        addr,
		Build:       build,
		MissingKeys: gaps,
		Mount:       proxy.Register,
		Ready: func(ctx context.Context) (bool, error) {
			status, err := configboot.FetchInstallStatus(ctx, boot.ConfigAPIURL, boot.HTTPClient)
			if err != nil {
				return false, err
			}
			return configboot.IsInstallReady(status.Status), nil
		},
	})
}
