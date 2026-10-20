package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/admin-frame/admin-frame/internal/platform"
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
func run(configPath string) error {
	return platform.Run(context.Background(), platform.Options{
		ConfigPath:    configPath,
		Build:         platform.BuildInfo{Version: version, Commit: commit, BuildTime: buildTime},
		NewAuthorizer: newAuthorizer,
		Register:      registerRoutes,
	})
}
