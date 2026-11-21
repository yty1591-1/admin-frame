package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	appconfig "github.com/admin-frame/admin-frame/cmd/internal/appconfig"
	"github.com/admin-frame/admin-frame/cmd/internal/configboot"
	iamruntime "github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	identityruntime "github.com/admin-frame/admin-frame/cmd/internal/identityruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/secretruntime"
	tenantruntime "github.com/admin-frame/admin-frame/cmd/internal/tenantruntime"
	identityconfig "github.com/admin-frame/admin-frame/internal/identity/config"
	"github.com/admin-frame/admin-frame/internal/platform"
	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
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
	if err := run(); err != nil {
		log.Printf("admin-api: %v", err)
		os.Exit(1)
	}
}

// run 只做装配声明；平台生命周期（含资源释放）由 platform.Run 统一负责。
//
// IAM 与身份域运行时各自在装配期构造一次：身份域的撤销检查器必须在 AuthMW
// 构造前返回（NewSessionValidator），路由注册复用同一实例。
func run() error {
	ctx := context.Background()
	boot, err := configboot.Load(ctx)
	if err != nil {
		return fmt.Errorf("config boot: %w", err)
	}
	// 先 Resolve 再 Validate：JWT / identity 明文只来自 secretclient。
	jwtSecret, err := secretruntime.ResolveJWTSecretFromEnv(ctx, boot.Env)
	if err != nil {
		return fmt.Errorf("jwt secret: %w", err)
	}
	hashKey, mfaSecret, err := secretruntime.ResolveIdentitySecretsFromEnv(ctx, boot.Env)
	if err != nil {
		if !platformconfig.IsLocalDevEnv(boot.Env) {
			return fmt.Errorf("identity secrets: %w", err)
		}
		// local/dev/test：允许回退到 applyLocalIdentityPIIKeys（CC-R-02）。
		hashKey, mfaSecret = "", ""
	}
	samlPrivateKey, _ := secretruntime.ResolveIdentitySAMLPrivateKeyFromEnv(ctx, boot.Env)
	subjectKey, _ := secretruntime.ResolveIdentitySubjectKeyFromEnv(ctx, boot.Env)
	appCfg, err := appconfig.LoadFromValues(func() (map[string]string, error) {
		// A 端登录依赖 identity + jwt；基线未播种 required issuer/audience，
		// 故进程级补齐非密钥项；密钥已由 secretclient 解析。
		// Env 必须在此注入，供 Normalize/Validate 使用（CC-R-01）。
		values := configboot.WithProcessIdentity(boot.Values, configboot.ProcessIdentity{
			Service: "admin-api", Channel: "admin", Env: boot.Env, Addr: ":8081",
		})
		// identity.enabled 与 channel/addr 同属进程能力：A 端进程必须启用登录路由；
		// 共享快照默认 false（且 worker 禁止启用），不能走 SetIfEmpty，否则冒烟 404。
		values["identity.enabled"] = "true"
		// issuer/mode/alg/audience：仅补齐快照缺失项（CC-R-15）。
		configboot.SetIfEmpty(values, "auth.mode", "jwt")
		configboot.SetIfEmpty(values, "auth.jwt.alg", "HS256")
		configboot.SetIfEmpty(values, "auth.jwt.issuer", "admin-frame")
		configboot.SetIfEmpty(values, "auth.jwt.audience", "admin")
		// prod/staging：IAM enforce / 审批 / redis 缓存等端别强制项。
		configboot.ApplyProdLikeProcessDefaults(values, boot.Env, "admin")
		configboot.ApplyDeployOverlays(values)
		values[secretruntime.JWTSecretValueKey] = jwtSecret
		if hashKey != "" {
			values[secretruntime.IdentityHashKeyValueKey] = hashKey
		}
		if mfaSecret != "" {
			values[secretruntime.IdentityMFASecretValueKey] = mfaSecret
		}
		// SAML 仅 channel=c；enabled=false 时注入私钥会触发 Validate 失败。
		if samlPrivateKey != "" && strings.EqualFold(values["identity.saml.enabled"], "true") {
			values[secretruntime.IdentitySAMLPrivateKeyValueKey] = samlPrivateKey
		}
		return values, nil
	})
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	// 主体加密：优先 secretclient 的 identity.subject.key；仍空时才本地回退（CC-R-05）。
	secretruntime.ApplyIdentitySubjectKey(&appCfg.Identity, subjectKey)
	// 仅 local/dev/test：配置中心未托管 hash_key / subject.keys 时注入固定开发密钥（CC-R-02）。
	applyLocalIdentityPIIKeys(appCfg.Env, &appCfg.Identity)
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
			return registerRoutes(deps, mux, built, identityBuilt, appCfg)
		},
	})
}

// applyLocalIdentityPIIKeys 在缺少托管密钥时填充本地 PII 摘要/加密密钥环。
//
// 仅 local/dev/test 且字段为空时写入；staging/prod/未声明环境一律跳过（CC-R-02）。
// 密钥为固定本地值，故意避开 validateSampleCredentials 拒绝的公开样例十六进制串。
func applyLocalIdentityPIIKeys(env string, cfg *identityconfig.IdentityConfig) {
	if cfg == nil || !cfg.Enabled {
		return
	}
	if !platformconfig.IsLocalDevEnv(env) {
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
