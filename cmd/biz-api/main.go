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
	iamruntime "github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	identityruntime "github.com/admin-frame/admin-frame/cmd/internal/identityruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/runtimebind"
	"github.com/admin-frame/admin-frame/cmd/internal/secretruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/snapshotconfig"
	tenantruntime "github.com/admin-frame/admin-frame/cmd/internal/tenantruntime"
	iamconfig "github.com/admin-frame/admin-frame/internal/iam/config"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
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
	configPath := flag.String("config", "configs/dev/biz.yaml", "config file path")
	flag.Parse()
	if err := run(*configPath); err != nil {
		log.Printf("biz-api: %v", err)
		os.Exit(1)
	}
}

// run 只做装配声明；平台生命周期由 platform.Run 统一负责。
//
// IAM 与身份域运行时各自在装配期构造一次：身份域的撤销检查器必须在 AuthMW
// 构造前返回（NewSessionValidator），路由注册复用同一实例。
func run(configPath string) error {
	// M1-9：本地签名快照优先于静态配置；未配置或验签失败时降级为文件配置。
	startup, err := snapshotconfig.Load(context.Background())
	if err != nil {
		log.Printf("biz-api: config snapshot ignored: %v", err)
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
		iamOnce     sync.Once
		iamRT       *iamruntime.Runtime
		iamErr      error
		identityOn  sync.Once
		identityRT  *identityruntime.Runtime
		identityErr error
		// tenantBind 由 NewAuthGuards 填充（buildGuards 先于 buildAuthN 执行），
		// identity 运行时在构造期读取，平台 Deps 不再承载业务语义函数字段。
		tenantBind runtimebind.Tenant
	)
	buildIAM := func(deps *platform.Deps) (*iamruntime.Runtime, error) {
		iamOnce.Do(func() { iamRT, iamErr = newIAMRuntime(deps, appCfg.IAM) })
		return iamRT, iamErr
	}
	buildIdentity := func(deps *platform.Deps) (*identityruntime.Runtime, error) {
		identityOn.Do(func() {
			identityRT, identityErr = identityruntime.New(deps, appCfg.Identity, tenantBind)
		})
		return identityRT, identityErr
	}
	return platform.Run(context.Background(), platform.Options{
		Config: &appCfg.Config,
		Build:  httpserver.BuildInfo{Version: version, Commit: commit, BuildTime: buildTime},
		// B 端是多租户通道：认证通过后还需确认商户仍在服务期（ENT-09），
		// 并执行 API 配额（ENT-14）。守卫顺序由 tenantruntime 显式声明：
		// 租户状态 → 配额，均包在 AuthMW 内层。
		NewAuthGuards: func(deps *platform.Deps) ([]func(http.Handler) http.Handler, error) {
			guards, bind, err := tenantruntime.AuthGuards(deps)
			if err != nil {
				return nil, err
			}
			tenantBind = bind
			return guards, nil
		},
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
		NewAuthorizer: func(deps *platform.Deps) (*security.Authorizer, error) {
			built, err := buildIAM(deps)
			if err != nil {
				return nil, err
			}
			return built.Authorizer, nil
		},
		Register: func(deps *platform.Deps, mux *http.ServeMux) error {
			built, err := buildIAM(deps)
			if err != nil {
				return err
			}
			identityBuilt, err := buildIdentity(deps)
			if err != nil {
				return err
			}
			// M1 迁移期双读：文件基线 vs 签名快照，差异只打点与记日志，不改变取值。
			if err := snapshotconfig.ReportWithMetrics(context.Background(), configPath,
				appCfg.Config.Env, startup, deps.Logger, deps.Metrics); err != nil {
				deps.Logger.Warn("configcenter dual read failed", "err", err)
			}
			// M2：可选密钥解析客户端（CONFIG_API_URL + CONFIG_SERVICE_IDENTITY）。
			if secretClient, serr := secretruntime.NewFromEnv(); serr != nil {
				deps.Logger.Warn("secretclient unavailable", "err", serr)
			} else if secretClient != nil {
				deps.Logger.Info("secretclient enabled", "base", "CONFIG_API_URL")
				if ferr := secretruntime.WarmDemoHMAC(context.Background(), secretClient,
					appCfg.Config.Env, deps.Logger); ferr != nil {
					deps.Logger.Warn("demo hmac secret warm failed", "err", ferr)
				}
			}
			// 动态刷新：快照可用性 / stale 指标 + ADR-0015 白名单键热更视图。
			if refreshRT, rerr := configcenterruntime.NewRefresh(deps); rerr != nil {
				deps.Logger.Warn("configcenter refresh unavailable", "err", rerr)
			} else if refreshRT != nil {
				refreshRT.StartRefresh(deps, 0)
				// ADR-0015：入口限流跟随热更视图，`rate_limit.*` 发布后无需重启。
				refreshRT.ApplyRuntimeParams(deps)
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
			return registerRoutes(deps, mux, built, identityBuilt)
		},
	})
}

// newIAMRuntime 装配 B 端 IAM，并注入 B 端静态回滚表。
func newIAMRuntime(deps *platform.Deps, iamCfg iamconfig.IAMConfig) (*iamruntime.Runtime, error) {
	return iamruntime.New(deps, iamCfg, iamruntime.Options{StaticChecker: staticChecker()})
}

// staticChecker 是 mode=off 的回滚表：当前不授予任何业务权限。
//
// 刻意不授予 IAM 管理权限点：B 端自助授权必须走 enforce + 真实绑定，
// 否则一次配置回滚（enforce → off）就会把商户管理员权限放大给所有员工。
// 后续接入 B 端业务域时，仅可在本表补充该域的只读回滚权限。
func staticChecker() *rbac.Checker {
	return rbac.New()
}
