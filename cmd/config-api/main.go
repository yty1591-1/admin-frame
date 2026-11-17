package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	appconfig "github.com/admin-frame/admin-frame/cmd/internal/appconfig"
	"github.com/admin-frame/admin-frame/cmd/internal/configcenterruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/secretruntime"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
)

var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

func main() {
	if err := run(); err != nil {
		log.Printf("config-api: %v", err)
		os.Exit(1)
	}
}

func run() error {
	// ADR-0016：config-api 自举，不依赖签名快照；仅代码默认值 + db/redis env。
	// service/channel/env 是平台进程身份证，内部服务写死，须在 Validate 前注入（CC-R-01）。
	env := strings.TrimSpace(os.Getenv("CONFIGCENTER_ENV"))
	addr := strings.TrimSpace(os.Getenv("CONFIG_API_ADDR"))
	if addr == "" {
		addr = ":8085"
	}
	appCfg, err := appconfig.LoadFromValues(func() (map[string]string, error) {
		values := map[string]string{
			"service": "config-api",
			"channel": "internal",
			"addr":    addr,
		}
		if env != "" {
			values["env"] = env
		}
		return values, nil
	})
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	tlsCfg, err := secretruntime.LoadServerTLSFromEnvFor(appCfg.Env)
	if err != nil {
		return fmt.Errorf("load tls: %w", err)
	}

	return platform.Run(context.Background(), platform.Options{
		Config:    &appCfg.Config,
		Build:     httpserver.BuildInfo{Version: version, Commit: commit, BuildTime: buildTime},
		TLSConfig: tlsCfg,
		Register: func(deps *platform.Deps, mux *http.ServeMux) error {
			rt, err := configcenterruntime.NewConfigAPI(deps, configcenterruntime.Options{Service: "config-api"})
			if err != nil {
				return err
			}
			if rt == nil || rt.Internal == nil {
				return fmt.Errorf("config-api internal runtime unavailable")
			}
			mux.Handle("GET /internal/v1/configs/{env}/{namespace}",
				http.HandlerFunc(rt.Internal.GetNamespaceConfig))
			mux.Handle("GET /internal/v1/configs/notifications",
				http.HandlerFunc(rt.Internal.Notifications))
			mux.Handle("POST /internal/v1/configs/secrets/resolve",
				http.HandlerFunc(rt.Internal.ResolveSecrets))
			return nil
		},
	})
}
