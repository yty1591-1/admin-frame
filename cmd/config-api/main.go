package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	appconfig "github.com/admin-frame/admin-frame/cmd/internal/appconfig"
	"github.com/admin-frame/admin-frame/cmd/internal/configboot"
	"github.com/admin-frame/admin-frame/cmd/internal/configcenterruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/secretruntime"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"

	// 安装态连通性探测需要云存储驱动（s3/minio 等）；与 admin-api 对齐。
	_ "github.com/admin-frame/admin-frame/pkg/storage/all"
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
	if env == "" {
		return fmt.Errorf("CONFIGCENTER_ENV is required (empty env bypasses prod-like guards)")
	}
	addr := strings.TrimSpace(os.Getenv("CONFIG_API_ADDR"))
	if addr == "" {
		addr = ":8085"
	}
	appCfg, err := appconfig.LoadFromValues(func() (map[string]string, error) {
		values := map[string]string{
			"service": "config-api",
			"channel": "internal",
			"addr":    addr,
			"env":     env,
		}
		configboot.ApplyConfigAPIBootstrapValues(values, env)
		return values, nil
	})
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// 内部通知允许挂起 60 秒；入口 context 与 socket 写入预算须覆盖等待和响应。
	appCfg.HTTP.Timeout = 65 * time.Second
	appCfg.HTTP.WriteTimeout = 70 * time.Second

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
			if rt.Install != nil {
				mux.Handle("GET /internal/v1/install/status",
					http.HandlerFunc(rt.Install.Status))
				mux.Handle("GET /internal/v1/install/schema",
					http.HandlerFunc(rt.Install.Schema))
				mux.Handle("POST /internal/v1/install/begin",
					http.HandlerFunc(rt.Install.Begin))
				mux.Handle("POST /internal/v1/install/apply",
					http.HandlerFunc(rt.Install.Apply))
				mux.Handle("POST /internal/v1/install/complete",
					http.HandlerFunc(rt.Install.Complete))
			}
			if rt.ResetDefaults != nil {
				mux.Handle("POST /internal/v1/configs/repair/reset-defaults",
					http.HandlerFunc(rt.ResetDefaults.ResetDefaults))
			}
			return nil
		},
	})
}
