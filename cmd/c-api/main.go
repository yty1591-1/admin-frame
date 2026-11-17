package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	appconfig "github.com/admin-frame/admin-frame/cmd/internal/appconfig"
	"github.com/admin-frame/admin-frame/cmd/internal/configboot"
	configcenterruntime "github.com/admin-frame/admin-frame/cmd/internal/configcenterruntime"
	identityruntime "github.com/admin-frame/admin-frame/cmd/internal/identityruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/runtimebind"
	"github.com/admin-frame/admin-frame/cmd/internal/secretruntime"
	"github.com/admin-frame/admin-frame/internal/platform"
	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
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
	if err := run(); err != nil {
		log.Printf("c-api: %v", err)
		os.Exit(1)
	}
}

// run 只做装配声明；平台生命周期由 platform.Run 统一负责。
//
// 身份域运行时在装配期构造一次：撤销检查器必须在 AuthMW 构造前返回
// （NewSessionValidator），路由注册复用同一实例。
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
		hashKey, mfaSecret = "", ""
	}
	samlPrivateKey, _ := secretruntime.ResolveIdentitySAMLPrivateKeyFromEnv(ctx, boot.Env)
	subjectKey, _ := secretruntime.ResolveIdentitySubjectKeyFromEnv(ctx, boot.Env)
	appCfg, err := appconfig.LoadFromValues(func() (map[string]string, error) {
		// Env 必须在此注入，供 Normalize/Validate 使用（CC-R-01）。
		values := configboot.WithProcessIdentity(boot.Values, configboot.ProcessIdentity{
			Service: "c-api", Channel: "c", Env: boot.Env, Addr: ":8083",
		})
		values[secretruntime.JWTSecretValueKey] = jwtSecret
		if hashKey != "" {
			values[secretruntime.IdentityHashKeyValueKey] = hashKey
		}
		if mfaSecret != "" {
			values[secretruntime.IdentityMFASecretValueKey] = mfaSecret
		}
		if samlPrivateKey != "" {
			values[secretruntime.IdentitySAMLPrivateKeyValueKey] = samlPrivateKey
		}
		return values, nil
	})
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	secretruntime.ApplyIdentitySubjectKey(&appCfg.Identity, subjectKey)
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
			// 动态刷新：快照可用性 / stale 指标 + ADR-0015 白名单键热更视图。
			if refreshRT, rerr := configcenterruntime.NewRefresh(deps); rerr != nil {
				deps.Logger.Warn("configcenter refresh unavailable", "err", rerr)
			} else if refreshRT != nil {
				refreshRT.StartRefresh(deps, 0)
				// ADR-0015：入口限流跟随热更视图，`rate_limit.*` 发布后无需重启。
				refreshRT.ApplyRuntimeParams(deps)
			}
			return registerRoutes(deps, mux, built)
		},
	})
}
