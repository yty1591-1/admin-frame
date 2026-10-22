package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/security"

	// 注册全部云存储驱动：admin-api 支持 memory / local / s3 / minio / oss / cos / obs。
	// 若部署确定不用多云，可改为只 import pkg/storage/s3，减少二进制与 CVE 面。
	_ "github.com/admin-frame/admin-frame/pkg/storage/all"
)

// 由 -ldflags -X 注入（RELS-03）；本地默认 dev。
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

func main() {
	configPath := flag.String("config", "configs/admin.yaml", "config file path")
	flag.Parse()
	if err := run(*configPath); err != nil {
		log.Printf("admin-api: %v", err)
		os.Exit(1)
	}
}

// run 只做装配声明；平台生命周期（含资源释放）由 platform.Run 统一负责。
//
// IAM 运行时在 NewAuthorizer 阶段构造一次，Register 复用同一实例，
// 保证管理端写入与鉴权读取共享同一套仓储 / 缓存。
func run(configPath string) error {
	var (
		once    sync.Once
		runtime *iamRuntime
		rtErr   error
	)
	build := func(deps *platform.Deps) (*iamRuntime, error) {
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
			// outbox 投递等后台任务随进程生命周期运行，关闭时先停投递再关数据库。
			for _, task := range built.Background {
				deps.Background(task)
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
