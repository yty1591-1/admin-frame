package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	appconfig "github.com/admin-frame/admin-frame/cmd/internal/appconfig"
	configcenterruntime "github.com/admin-frame/admin-frame/cmd/internal/configcenterruntime"
	identityruntime "github.com/admin-frame/admin-frame/cmd/internal/identityruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/runtimebind"
	"github.com/admin-frame/admin-frame/cmd/internal/snapshotconfig"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"

	// 注册全部云存储驱动；C 端如需独立裁剪可只 import 所需驱动。
	_ "github.com/admin-frame/admin-frame/pkg/storage/all"
	// 注册阿里云短信驱动，供 identity.sms.driver=aliyun 使用。
	_ "github.com/admin-frame/admin-frame/pkg/sms/aliyun"
)

// 由 -ldflags -X 注入（RELS-03）；本地默认 dev。
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

func main() {
	configPath := flag.String("config", "configs/dev/c.yaml", "config file path")
	flag.Parse()
	if err := run(*configPath); err != nil {
		log.Printf("c-api: %v", err)
		os.Exit(1)
	}
}

// run 只做装配声明；平台生命周期由 platform.Run 统一负责。
//
// 身份域运行时在装配期构造一次：撤销检查器必须在 AuthMW 构造前返回
// （NewSessionValidator），路由注册复用同一实例。
func run(configPath string) error {
	// M1-9：本地签名快照优先于静态配置；未配置或验签失败时降级为文件配置。
	startup, err := snapshotconfig.Load(context.Background())
	if err != nil {
		log.Printf("c-api: config snapshot ignored: %v", err)
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
	var (
		identityOnce sync.Once
		identityRT   *identityruntime.Runtime
		identityErr  error
	)
	buildIdentity := func(deps *platform.Deps) (*identityruntime.Runtime, error) {
		// C 端不装配租户守卫：租户状态 / 成员关系判定不适用，传空绑定。
		identityOnce.Do(func() {
			identityRT, identityErr = identityruntime.New(deps, appCfg.Identity, runtimebind.Tenant{})
		})
		return identityRT, identityErr
	}
	return platform.Run(context.Background(), platform.Options{
		Config: &appCfg.Config,
		Build:  httpserver.BuildInfo{Version: version, Commit: commit, BuildTime: buildTime},
		NewSessionValidator: func(deps *platform.Deps) (platform.SessionValidator, error) {
			built, err := buildIdentity(deps)
			if err != nil {
				return nil, err
			}
			if built == nil || built.Checker == nil {
				return nil, nil
			}
			return built.Checker, nil
		},
		Register: func(deps *platform.Deps, mux *http.ServeMux) error {
			built, err := buildIdentity(deps)
			if err != nil {
				return err
			}
			// M1 迁移期双读：文件基线 vs 签名快照，差异只打点与记日志，不改变取值。
			if err := snapshotconfig.ReportWithMetrics(context.Background(), configPath,
				appCfg.Config.Env, startup, deps.Logger, deps.Metrics); err != nil {
				deps.Logger.Warn("configcenter dual read failed", "err", err)
			}
			// 动态刷新只提供快照可用性 / stale 指标；热更值回写尚未启用。
			if refreshRT, rerr := configcenterruntime.NewRefresh(deps); rerr != nil {
				deps.Logger.Warn("configcenter refresh unavailable", "err", rerr)
			} else if refreshRT != nil {
				refreshRT.StartRefresh(deps, 0)
				// 迁移期双读：每 30s 跟随快照刷新采样一次，支撑 7 天零差异取证。
				if refreshRT.Client != nil {
					snapshotconfig.StartPeriodic(deps.Background, snapshotconfig.PeriodicConfig{
						ConfigPath: configPath,
						Env:        appCfg.Config.Env,
						Reader:     refreshRT.Client,
						Logger:     deps.Logger,
						Metrics:    deps.Metrics,
					})
				}
			}
			return registerRoutes(deps, mux, built)
		},
	})
}
