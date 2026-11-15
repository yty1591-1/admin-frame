// Command config-api 是配置中心内部密钥解析服务（M2 / ADR-0014）。
//
// 仅暴露 /internal/v1/configs/secrets/resolve 与健康检查；不承载管理面。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	appconfig "github.com/admin-frame/admin-frame/cmd/internal/appconfig"
	"github.com/admin-frame/admin-frame/cmd/internal/configcenterruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/secretruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/snapshotconfig"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
)

var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

func main() {
	configPath := flag.String("config", "configs/dev/admin.yaml", "config file path (shared platform bootstrap)")
	flag.Parse()
	if err := run(*configPath); err != nil {
		log.Printf("config-api: %v", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	startup, err := snapshotconfig.Load(context.Background())
	if err != nil {
		log.Printf("config-api: config snapshot ignored: %v", err)
	}
	var snapshotValues map[string]string
	if startup != nil {
		snapshotValues = startup.Values
	}
	appCfg, err := appconfig.LoadWithValuesAllowMissing(configPath, func() (map[string]string, error) {
		return snapshotValues, nil
	}, snapshotconfig.AllowMissingConfig(snapshotValues))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if addr := strings.TrimSpace(os.Getenv("CONFIG_API_ADDR")); addr != "" {
		appCfg.Addr = addr
	} else if appCfg.Addr == "" || appCfg.Addr == ":8081" || appCfg.Addr == ":8080" {
		appCfg.Addr = ":8085"
	}
	appCfg.Service = "config-api"

	tlsCfg, err := secretruntime.LoadServerTLSFromEnv()
	if err != nil {
		return fmt.Errorf("load tls: %w", err)
	}

	return platform.Run(context.Background(), platform.Options{
		Config:    &appCfg.Config,
		Build:     httpserver.BuildInfo{Version: version, Commit: commit, BuildTime: buildTime},
		TLSConfig: tlsCfg,
		Register: func(deps *platform.Deps, mux *http.ServeMux) error {
			rt, err := configcenterruntime.NewSecretResolve(deps, configcenterruntime.Options{Service: "config-api"})
			if err != nil {
				return err
			}
			if rt == nil || rt.Handler == nil {
				return fmt.Errorf("config-api secret runtime unavailable")
			}
			mux.Handle("POST /internal/v1/configs/secrets/resolve",
				http.HandlerFunc(rt.Handler.ResolveSecrets))
			return nil
		},
	})
}
