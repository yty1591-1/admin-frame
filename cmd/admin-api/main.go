package main

import (
	"context"
	"errors"
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
		if boot.InstallPending {
			jwtSecret = configboot.InstallPendingJWTSecret
		} else {
			return fmt.Errorf("jwt secret: %w", err)
		}
	}
	hashKey, mfaSecret, err := secretruntime.ResolveIdentitySecretsFromEnv(ctx, boot.Env)
	if err != nil {
		if boot.InstallPending || platformconfig.IsLocalDevEnv(boot.Env) {
			// 安装未完成或 local/dev：回退空值，由 applyLocalIdentityPIIKeys / 占位完成监听。
			hashKey, mfaSecret = "", ""
		} else {
			return fmt.Errorf("identity secrets: %w", err)
		}
	}
	samlPrivateKey, _ := secretruntime.ResolveIdentitySAMLPrivateKeyFromEnv(ctx, boot.Env)
	subjectKey, _ := secretruntime.ResolveIdentitySubjectKeyFromEnv(ctx, boot.Env)
	// 对象存储凭据：走密钥子域（config_secret）；仅在 s3/minio 驱动时解析，
	// 解析失败不在此处退出——交由存储装配阶段判定，从而能降级到安装向导。
	storageAK, storageSK, _ := secretruntime.ResolveStorageS3CredentialsFromEnv(ctx, boot.Env)
	redisPassword, _ := secretruntime.ResolveRedisPasswordFromEnv(ctx, boot.Env)
	asyncBrokerURL, _ := secretruntime.ResolveAsyncBrokerURLFromEnv(ctx, boot.Env)
	// gaps 在装配退出时回传：缺必填键时改走 setup-only，避免带病装配（ADR-0018）。
	var bootGaps []string
	var bootValues map[string]string
	gapsKnown := false
	// validateValues 用于失败后复判：填占位值能过校验，说明失败是「缺值」而非「值写错」。
	validateValues := func(values map[string]string) error {
		_, verr := appconfig.LoadFromValues(func() (map[string]string, error) {
			return values, nil
		})
		return verr
	}
	buildValues := func() (map[string]string, error) {
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
		// 对象存储凭据来自 config_secret，覆盖部署侧 env（CC-R-05）。
		if storageAK != "" {
			values[secretruntime.StorageS3AccessKeyValueKey] = storageAK
		}
		if storageSK != "" {
			values[secretruntime.StorageS3SecretKeyValueKey] = storageSK
		}
		if redisPassword != "" {
			values[secretruntime.RedisPasswordValueKey] = redisPassword
		}
		if asyncBrokerURL != "" {
			values[secretruntime.AsyncBrokerURLValueKey] = asyncBrokerURL
		}
		gaps, gapsErr := configboot.MissingRequired(values)
		if gapsErr != nil {
			return nil, gapsErr
		}
		bootGaps, gapsKnown = gaps, true
		bootValues = values
		return values, nil
	}
	appCfg, err := appconfig.LoadFromValues(buildValues)
	build := httpserver.BuildInfo{Version: version, Commit: commit, BuildTime: buildTime}
	if gapsKnown && len(bootGaps) > 0 {
		if err != nil {
			log.Printf("admin-api: config incomplete (%v); entering setup-only mode: %s", err, strings.Join(bootGaps, ", "))
		} else {
			log.Printf("admin-api: missing required keys; entering setup-only mode: %s", strings.Join(bootGaps, ", "))
		}
		return runSetupOnly(ctx, boot, bootGaps, build)
	}
	if err != nil {
		// 注册表看不出缺键（例如只有配置校验层才要求的值）时再判一次：
		// 填占位值能过校验即视为「缺值阻塞」；只能把值改回声明默认值才过则视为
		// 「值非法」（ADR-0018 决策 3 / 待定 1）。两者都判不出则保持 fail-fast。
		heal := healStartupConfig(ctx, boot, bootValues, err, validateValues, configboot.Load)
		if len(heal.Gaps) > 0 {
			log.Printf("admin-api: config validation failed (%v); missing values block startup, entering setup-only mode: %s",
				err, strings.Join(heal.Gaps, ", "))
			return runSetupOnly(ctx, boot, heal.Gaps, build)
		}
		// 自愈成功后重试一次装配；仍不通过则保留 fail-fast，不带病启动。
		if heal.Reloaded {
			boot = heal.Boot
			if cfg, retryErr := appconfig.LoadFromValues(buildValues); retryErr != nil {
				log.Printf("admin-api: config still invalid after reset (%v); keeping fail-fast", retryErr)
			} else {
				appCfg, err = cfg, nil
			}
		}
	}
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
	runErr := platform.Run(context.Background(), platform.Options{
		Config: &appCfg.Config,
		Build:  build,
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
			configboot.RegisterInstallReadyCheck(deps.Health, boot.ConfigAPIURL, boot.HTTPClient)
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
	// 对象存储装配失败（缺桶/区域、凭据无效、端点不可达）不退出：降级为
	// setup-only，让运维经 /setup/install 重新配置对象存储后重启。
	// 其余装配错误保持 fail-fast，不带病运行（ADR-0018 决策 3）。
	if runErr != nil && errors.Is(runErr, platform.ErrStorageUnavailable) {
		gaps := storageHealGaps()
		log.Printf("admin-api: storage unavailable (%v); entering setup-only mode to reconfigure storage: %s",
			runErr, strings.Join(gaps, ", "))
		return runSetupOnly(context.Background(), boot, gaps, build)
	}
	return runErr
}

// storageHealGaps 返回对象存储自愈时需在向导中确认的键（namespace/key）。
//
// 只列非敏感连接参数：AK/SK 由向导按需填写，且任何接口都不回显，
// 列入 gaps 只会让 /readyz 文案泄露键名而无助于排障。
func storageHealGaps() []string {
	return []string{
		"platform/storage.driver",
		"platform/storage.s3.bucket",
		"platform/storage.s3.region",
	}
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
