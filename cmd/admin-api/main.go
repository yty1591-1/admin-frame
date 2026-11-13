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
	iamruntime "github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	identityruntime "github.com/admin-frame/admin-frame/cmd/internal/identityruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/snapshotconfig"
	tenantruntime "github.com/admin-frame/admin-frame/cmd/internal/tenantruntime"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
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
	configPath := flag.String("config", "configs/dev/admin.yaml", "config file path")
	flag.Parse()
	if err := run(*configPath); err != nil {
		log.Printf("admin-api: %v", err)
		os.Exit(1)
	}
}

// run 只做装配声明；平台生命周期（含资源释放）由 platform.Run 统一负责。
//
// IAM 与身份域运行时各自在装配期构造一次：身份域的撤销检查器必须在 AuthMW
// 构造前返回（NewSessionValidator），路由注册复用同一实例。
func run(configPath string) error {
	// M1：本地签名快照优先于静态配置；未配置或验签失败时降级为文件配置。
	startup, err := snapshotconfig.Load(context.Background())
	if err != nil {
		log.Printf("admin-api: config snapshot ignored: %v", err)
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
		iamRT       *iamRuntime
		iamErr      error
		identityOn  sync.Once
		identityRT  *identityruntime.Runtime
		identityErr error
	)
	buildIAM := func(deps *platform.Deps) (*iamRuntime, error) {
		iamOnce.Do(func() { iamRT, iamErr = newIAMRuntime(deps, appCfg.IAM) })
		return iamRT, iamErr
	}
	buildIdentity := func(deps *platform.Deps) (*identityruntime.Runtime, error) {
		identityOn.Do(func() {
			// T-22：成员关系绑定必须先于 identity 构造取得（切换端点依赖）；
			// 绑定经 cmd 显式传入，不写回 platform.Deps。
			bind, mErr := tenantruntime.MembershipBinding(deps)
			if mErr != nil {
				identityErr = mErr
				return
			}
			var identityOpts []identityruntime.Option
			if appCfg.Identity.SCIM.Enabled {
				// SCIM 角色投影实现在 cmd 层：身份域不 import IAM（§13.1）。
				provisioner, pErr := iamruntime.NewScimRoleProvisioner(deps, appCfg.IAM)
				if pErr != nil {
					identityErr = pErr
					return
				}
				identityOpts = append(identityOpts, identityruntime.WithScimRoleProvisioner(provisioner))
			}
			identityRT, identityErr = identityruntime.New(deps, appCfg.Identity, bind, identityOpts...)
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
			return registerRoutes(deps, mux, built, identityBuilt, appCfg)
		},
	})
}
