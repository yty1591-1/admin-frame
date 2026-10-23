package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"sync"

	iamruntime "github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/pkg/rbac"

	// 注册全部云存储驱动；不需要多云时可按需改为单一驱动包。
	_ "github.com/admin-frame/admin-frame/pkg/storage/all"
)

// 由 -ldflags -X 注入（RELS-03）；本地默认 dev。
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

func main() {
	configPath := flag.String("config", "configs/biz.yaml", "config file path")
	flag.Parse()
	if err := run(*configPath); err != nil {
		log.Printf("biz-api: %v", err)
		os.Exit(1)
	}
}

// run 只做装配声明；平台生命周期由 platform.Run 统一负责。
//
// IAM 运行时在 NewAuthorizer 阶段构造一次，Register 复用同一实例，
// 保证 B 端自助管理写入与鉴权读取共享同一套仓储 / 缓存。
func run(configPath string) error {
	var (
		once    sync.Once
		runtime *iamruntime.Runtime
		rtErr   error
	)
	build := func(deps *platform.Deps) (*iamruntime.Runtime, error) {
		once.Do(func() { runtime, rtErr = newIAMRuntime(deps) })
		return runtime, rtErr
	}
	return platform.Run(context.Background(), platform.Options{
		ConfigPath: configPath,
		Build:      platform.BuildInfo{Version: version, Commit: commit, BuildTime: buildTime},
		NewAuthorizer: func(deps *platform.Deps) (*security.Authorizer, error) {
			built, err := build(deps)
			if err != nil {
				return nil, err
			}
			return built.Authorizer, nil
		},
		Register: func(deps *platform.Deps, mux *http.ServeMux) error {
			built, err := build(deps)
			if err != nil {
				return err
			}
			return registerRoutes(deps, mux, built)
		},
	})
}

// newIAMRuntime 装配 B 端 IAM，并注入 B 端静态回滚表。
func newIAMRuntime(deps *platform.Deps) (*iamruntime.Runtime, error) {
	return iamruntime.New(deps, iamruntime.Options{StaticChecker: staticChecker()})
}

// staticChecker 是 mode=off 的回滚表：当前不授予任何业务权限。
//
// 刻意不授予 IAM 管理权限点：B 端自助授权必须走 enforce + 真实绑定，
// 否则一次配置回滚（enforce → off）就会把商户管理员权限放大给所有员工。
// 后续接入 B 端业务域时，仅可在本表补充该域的只读回滚权限。
func staticChecker() *rbac.Checker {
	return rbac.New()
}
