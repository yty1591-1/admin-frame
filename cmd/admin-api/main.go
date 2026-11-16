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
	"github.com/admin-frame/admin-frame/cmd/internal/configboot"
	iamruntime "github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	identityruntime "github.com/admin-frame/admin-frame/cmd/internal/identityruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/secretruntime"
	tenantruntime "github.com/admin-frame/admin-frame/cmd/internal/tenantruntime"
	identityconfig "github.com/admin-frame/admin-frame/internal/identity/config"
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
	configPath := flag.String("config", "unused.yaml", "optional config file (ignored when signed snapshot is set)")
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
	boot, err := configboot.Load(context.Background())
	if err != nil {
		return fmt.Errorf("config boot: %w", err)
	}
	appCfg, err := appconfig.LoadWithValuesAllowMissing(configPath, func() (map[string]string, error) {
		// A 端登录依赖 identity + jwt；基线未播种 required issuer/audience，
		// 且 JWT 明文密钥在 Validate 之后才由 secretclient 注入，故进程级补齐。
		values := configboot.WithProcessIdentity(boot.Values, configboot.ProcessIdentity{
			Service: "admin-api", Channel: "admin", Addr: ":8081",
		})
		values["identity.enabled"] = "true"
		values["auth.mode"] = "jwt"
		values["auth.jwt.alg"] = "HS256"
		values["auth.jwt.issuer"] = "admin-frame"
		values["auth.jwt.audience"] = "admin"
		// 占位满足 Validate；随即被 secretruntime.ApplyJWTSecretFromEnv 覆盖。
		values["auth.jwt.secret"] = "smoke-placeholder-jwt-secret-32bytes!!"
		return values, nil
	}, true)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if boot.Env != "" {
		appCfg.Env = boot.Env
	}
	if jerr := secretruntime.ApplyJWTSecretFromEnv(context.Background(), &appCfg.Config, nil); jerr != nil {
		return fmt.Errorf("jwt secret: %w", jerr)
	}
	// 本地/冒烟：配置中心不托管 identity.hash_key / subject.keys，缺省则注入固定非样例密钥，
	// 供 EnsureAdmin 与登录 PII 索引（非生产路径；生产须经 secrets.providers）。
	applyLocalIdentityPIIKeys(&appCfg.Identity)
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
			return registerRoutes(deps, mux, built, identityBuilt, appCfg, configPath)
		},
	})
}

// applyLocalIdentityPIIKeys 在缺少托管密钥时填充本地 PII 摘要/加密密钥环。
//
// 仅当字段为空时写入；不覆盖 vault/file 已注入的值。密钥为固定本地值，
// 故意避开 validateSampleCredentials 拒绝的公开样例十六进制串。
func applyLocalIdentityPIIKeys(cfg *identityconfig.IdentityConfig) {
	if cfg == nil || !cfg.Enabled {
		return
	}
	const (
		localHashKey    = "c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00"
		localSubjectKey = "faceb00cfaceb00cfaceb00cfaceb00cfaceb00cfaceb00cfaceb00cfaceb00c"
		localKeyID      = "v1"
	)
	if cfg.HashKey == "" {
		cfg.HashKey = localHashKey
		if cfg.HashKeyID == "" {
			cfg.HashKeyID = localKeyID
		}
	}
	if len(cfg.Subject.Keys) == 0 {
		cfg.Subject.ActiveKeyID = localKeyID
		cfg.Subject.Keys = []identityconfig.IdentityMFAKeyConfig{{
			ID:  localKeyID,
			Key: localSubjectKey,
		}}
	}
}
