package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/admin-frame/admin-frame/internal/platform"
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
func run(configPath string) error {
	return platform.Run(context.Background(), platform.Options{
		ConfigPath:    configPath,
		Build:         platform.BuildInfo{Version: version, Commit: commit, BuildTime: buildTime},
		NewAuthorizer: newAuthorizer,
		Register:      registerRoutes,
	})
}
