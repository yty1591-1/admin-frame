package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform/dependency"
	dependencyobservability "github.com/admin-frame/admin-frame/internal/platform/dependency/observability"
	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/health"
	pkghttpx "github.com/admin-frame/admin-frame/pkg/httpx"
	"github.com/admin-frame/admin-frame/pkg/id"
	"github.com/admin-frame/admin-frame/pkg/jwt"
	"github.com/admin-frame/admin-frame/pkg/metrics"
	"github.com/admin-frame/admin-frame/pkg/redis"
	"github.com/admin-frame/admin-frame/pkg/storage"
	"github.com/admin-frame/admin-frame/pkg/tracing"
)

// Deps 是装配期注入给 cmd 的平台能力集合。
//
// 只暴露“平台能力”，不暴露 repo / bus 等业务对象，避免变成 god object；
// 业务依赖由 cmd 在 Register 回调内自行构造。
type Deps struct {
	Config  *Config
	Logger  *slog.Logger
	Metrics *metrics.Metrics
	Tracer  *tracing.Provider
	Storage *StorageSet
	// Database 是进程数据库；db.driver=memory 时为 nil（业务使用内存实现）。
	Database *db.DB
	// Redis 是共享缓存客户端；未配置 redis.addr 时为 nil。
	Redis *redis.Client
	// IDs 是进程级分布式 ID 生成器（多副本必须使用不同 NodeID）。
	IDs id.Generator
	// AuthMW 是已按 channel 绑定的鉴权中间件。
	AuthMW func(http.Handler) http.Handler
	// RequirePermission 是权限中间件工厂，由 cmd 注入的 Authorizer 提供。
	RequirePermission auth.PermissionMiddleware
	// TenantGuard 是租户状态守卫（ENT-09）；nil 表示本端不做租户校验。
	TenantGuard TenantGuard
	// QuotaGuard 是租户 API 配额守卫（ENT-14）；nil 表示本端不执行配额。
	QuotaGuard QuotaGuard
	// Dependencies 是外部依赖注册表（ENT-13）：出站依赖（第三方 HTTP API
	// 等）在装配期按名登记超时 / 重试 / 熔断，运行期经 Caller.Do 执行并
	// 出指标。nil 仅出现在单测形态。
	Dependencies *dependency.Registry
	// TenantMembership 判定主体是否为租户在册成员（ENT-09 T-22 切换端点）；
	// nil 表示本端不提供成员关系查询（切换端点将拒绝服务）。
	TenantMembership TenantMembership
	// Health 用于 Register 阶段追加就绪检查（如仓储 ping）。
	Health *health.Handler
	// ClientIP 是可信代理解析器；nil 表示不信任任何代理头。
	ClientIP *pkghttpx.ClientIPResolver
	// Background 注册进程级后台任务（如 IAM outbox 投递）。
	//
	// 只允许在装配阶段（NewAuthorizer / Register）调用；任务在路由注册完成后启动，
	// ctx 在进程退出、HTTP 优雅关闭开始后取消。任务返回错误只记录日志，不改变退出码。
	Background func(task func(context.Context) error)
	// Async 是通用异步基础设施（ENT-03）；async.enabled=false 时为 nil。
	//
	// 业务域通过 Async.Register 声明 topic 与 handler；API 进程仅在
	// async.outbox.publish_in_api=true 时启动 Relay。
	Async *AsyncRuntime
}

// SessionValidator 是撤销检查的最小接口，方法签名与 security.RevocationChecker 一致。
//
// platform 只依赖该接口，不反向依赖 identity 域；由 cmd 在装配期注入实现。
type SessionValidator interface {
	Check(ctx context.Context, claims jwt.Claims) error
}

// Options 是 platform.Run 的装配参数。
type Options struct {
	// ConfigPath 是 yaml 路径；环境变量可覆盖其中字段（CFG-04）。
	ConfigPath string
	// Build 是构建身份；Service 为空时取配置中的 service。
	Build BuildInfo
	// NewSessionValidator 构造撤销检查器；identity.enabled=false 时返回 nil。
	//
	// 该回调在 AuthMW 构造前调用，使登录态校验能立即读取撤销状态。
	NewSessionValidator func(*Deps) (SessionValidator, error)
	// NewTenantGuard 构造租户状态守卫；返回 nil 表示本端不启用（A 端 / C 端）。
	//
	// 在 AuthMW 组装前调用：守卫是鉴权链的一环，必须与 AuthMW 一起装配，
	// 否则「认证已通过但租户已停用」的请求会漏到业务处理器。
	NewTenantGuard func(*Deps) (TenantGuard, error)
	// NewQuotaGuard 构造租户 API 配额守卫（ENT-14）；返回 nil 表示本端不执行。
	//
	// 与 NewTenantGuard 同一批组装：配额守卫包在租户状态守卫内层，
	// 已停用租户不应再消耗配额判定。
	NewQuotaGuard func(*Deps) (QuotaGuard, error)
	// NewAuthorizer 构造权限校验器；返回 nil 或 nil 函数时默认拒绝所有权限点（fail-closed）。
	//
	// 参数为已装配的平台能力（配置 / 数据库 / Redis / 指标 / 健康检查），
	// 使 cmd 可在同一处构造 IAM 快照加载器并复用其仓储与缓存。
	NewAuthorizer func(*Deps) (*security.Authorizer, error)
	// Register 注册本端 transport 与业务依赖；不得注册他端路由。
	Register func(*Deps, *http.ServeMux) error
}

// Run 完成一次完整的进程装配与运行：配置 → 日志 → 指标 → 追踪 → 存储 →
// 鉴权 → 请求链路 → 探针 → 监听 → 优雅关闭 → 资源释放。
//
// 任一阶段失败返回 error，由 cmd 决定退出码；本函数不使用 log.Fatal / os.Exit，
// 保证 defer 清理一定执行（REL-02）。
func Run(ctx context.Context, opts Options) (err error) {
	if opts.Register == nil {
		return errors.New("platform: Options.Register is required")
	}
	cfg, err := LoadConfig(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := NewLogger(cfg)

	ids, nodeID, err := NewIDGenerator(cfg.ID)
	if err != nil {
		return fmt.Errorf("id generator: %w", err)
	}

	met, err := metrics.New(metrics.Options{
		Service: cfg.Service,
		Channel: cfg.Channel,
		Env:     cfg.Env,
	})
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	build := opts.Build
	if build.Service == "" {
		build.Service = cfg.Service
	}
	if err := met.RegisterBuildInfo(build.Version, build.Commit, build.BuildTime); err != nil {
		return fmt.Errorf("metrics build info: %w", err)
	}

	tp, err := tracing.Init(ctx, cfg.TracingOptions(build.Version))
	if err != nil {
		return fmt.Errorf("tracing: %w", err)
	}
	defer func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := tp.Shutdown(shCtx); shutdownErr != nil && err == nil {
			err = fmt.Errorf("tracing shutdown: %w", shutdownErr)
		}
	}()

	stores, err := OpenStorageSet(ctx, cfg.Storage)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() {
		if closeErr := stores.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("storage close: %w", closeErr)
		}
	}()

	database, err := OpenDatabase(ctx, cfg.DB, tp.TracerProvider())
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("db close: %w", closeErr)
		}
	}()
	if err := RegisterDBMetrics(met.RegistererWithoutChannel(), database); err != nil {
		return fmt.Errorf("db metrics: %w", err)
	}

	redisClient, err := OpenRedis(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := redisClient.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("redis close: %w", closeErr)
		}
	}()

	hh := health.New()
	registerStorageChecks(hh, stores)
	if database != nil {
		hh.Register("db", database.Checker())
	}
	if redisClient != nil {
		hh.Register("redis", redisClient.Checker())
	}

	clientIPResolver, err := pkghttpx.NewClientIPResolver(cfg.HTTP.TrustedProxies)
	if err != nil {
		return fmt.Errorf("trusted proxies: %w", err)
	}

	background := &backgroundTasks{}
	deps := &Deps{
		Config:     cfg,
		Logger:     logger,
		Metrics:    met,
		Tracer:     tp,
		Storage:    stores,
		Database:   database,
		Redis:      redisClient,
		IDs:        ids,
		ClientIP:   clientIPResolver,
		Health:     hh,
		Background: background.add,
	}
	// ENT-13：外部依赖注册表——出站依赖统一登记（超时 / 重试 / 熔断 +
	// dependency_* 指标）；指标缺失时 Observer 为 noop（app 层归一）。
	var depObserver dependency.Observer
	if met != nil {
		obs, obsErr := dependencyobservability.NewMetrics(met.RegistererWithoutChannel())
		if obsErr != nil {
			return fmt.Errorf("dependency observability: %w", obsErr)
		}
		depObserver = obs
	}
	deps.Dependencies = dependency.NewRegistry(depObserver)

	// 异步运行时必须先于身份运行时构造：身份域启用时审计写入 identity.audit Outbox，
	// 装配期需要可见 deps.Async（禁用时为空操作）。
	asyncRuntime, err := NewAsyncRuntime(deps)
	if err != nil {
		return fmt.Errorf("async runtime: %w", err)
	}
	deps.Async = asyncRuntime
	if asyncRuntime != nil && asyncRuntime.PublishInAPI() {
		background.add(asyncRuntime.Run)
	}

	// 租户守卫先于身份运行时构造：登录（T-08）与账号开通（T-10）都要在
	// identity 装配期读到守卫；守卫自身只依赖数据库，不反向依赖身份域。
	if opts.NewTenantGuard != nil {
		guard, guardErr := opts.NewTenantGuard(deps)
		if guardErr != nil {
			return fmt.Errorf("tenant guard: %w", guardErr)
		}
		if guard != nil {
			deps.TenantGuard = guard
		}
	}
	// ENT-14：API 配额守卫与租户状态守卫同批组装（中间件链内层）。
	if opts.NewQuotaGuard != nil {
		quota, quotaErr := opts.NewQuotaGuard(deps)
		if quotaErr != nil {
			return fmt.Errorf("quota guard: %w", quotaErr)
		}
		if quota != nil {
			deps.QuotaGuard = quota
		}
	}

	// 撤销检查必须在 AuthMW 构造前注入（docs/identity-authn.md §13.2）：
	// 身份运行时同时持有仓储与撤销存储，登录态校验才能立即读取撤销状态。
	var authOpts []security.JWTAuthOption
	if opts.NewSessionValidator != nil {
		validator, validatorErr := opts.NewSessionValidator(deps)
		if validatorErr != nil {
			return fmt.Errorf("session validator: %w", validatorErr)
		}
		if validator != nil {
			authOpts = append(authOpts, security.WithRevocationChecker(validator))
		}
	}

	authMW, err := NewAuthMiddleware(cfg.Auth, auth.Channel(cfg.Channel), cfg.Env, authOpts...)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	// 守卫包在鉴权中间件内层：鉴权先写入 Principal，守卫再读 merchant_id。
	// B 端令牌必须携带商户：缺失即签发缺陷，fail-closed（T-09）；
	// A/C 端平台级请求没有商户，直接放行。
	if deps.TenantGuard != nil {
		requireMerchant := deps.Config.Channel == "biz"
		authMW = WithTenantGuard(authMW, deps.TenantGuard, requireMerchant)
		// ENT-14：API 配额守卫包在租户状态守卫内层——已停用租户
		// 在上一环就被 403，不应再消耗配额判定。
		if deps.QuotaGuard != nil {
			authMW = WithQuotaGuard(authMW, deps.QuotaGuard)
		}
	}
	deps.AuthMW = authMW

	authorizer := security.NewAuthorizer(nil)
	if opts.NewAuthorizer != nil {
		built, buildErr := opts.NewAuthorizer(deps)
		if buildErr != nil {
			return fmt.Errorf("authorizer: %w", buildErr)
		}
		if built != nil {
			authorizer = built
		}
	}
	deps.RequirePermission = authorizer.Require

	api := http.NewServeMux()
	if err := opts.Register(deps, api); err != nil {
		return fmt.Errorf("register routes: %w", err)
	}

	root := http.NewServeMux()
	MountHealth(root, hh)
	MountVersion(root, build)
	MountMetrics(root, met.Handler())
	root.Handle("/", api)

	pipeline, err := NewPipeline(PipelineConfig{
		Timeout:        cfg.HTTP.Timeout,
		BodyLimitBytes: cfg.HTTP.BodyLimitBytes,
		Channel:        cfg.Channel,
		RateLimit:      cfg.RateLimit,
		Idempotency:    cfg.Idempotency,
		CORS:           cfg.CORS,
	}, PipelineDeps{
		Logger:   logger,
		Metrics:  met,
		Tracer:   tp,
		Redis:    redisClient,
		ClientIP: clientIPResolver,
	})
	if err != nil {
		return err
	}

	tasks := background.list()
	stopBackground := startBackground(ctx, tasks, logger)
	defer stopBackground()

	logger.Info("process starting",
		"service", cfg.Service,
		"channel", cfg.Channel,
		"env", cfg.Env,
		"version", build.Version,
		"node_id", nodeID,
		"background_tasks", len(tasks),
	)
	return ListenAndServe(ctx, cfg, pipeline(root), logger)
}

// backgroundTasks 收集装配期注册的后台任务；启动后不再接受注册。
type backgroundTasks struct {
	mu    sync.Mutex
	tasks []func(context.Context) error
}

func (b *backgroundTasks) add(task func(context.Context) error) {
	if task == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tasks = append(b.tasks, task)
}

func (b *backgroundTasks) list() []func(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]func(context.Context) error(nil), b.tasks...)
}

// startBackground 启动进程级后台任务，返回 stop：取消并等待全部退出。
//
// 任务错误只记录日志：后台任务失败不能把已经正常关闭的进程变成启动失败，
// 但必须可观测（日志 + 对应业务指标），否则 outbox 停摆会静默丢审计。
func startBackground(ctx context.Context, tasks []func(context.Context) error, logger *slog.Logger) func() {
	bgCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for _, task := range tasks {
		if task == nil {
			continue
		}
		wg.Add(1)
		go func(task func(context.Context) error) {
			defer wg.Done()
			if err := task(bgCtx); err != nil && bgCtx.Err() == nil {
				logger.Error("background task stopped", "err", err)
			}
		}(task)
	}
	return func() {
		cancel()
		wg.Wait()
	}
}

// registerStorageChecks 为默认桶与命名桶注册就绪探测。
func registerStorageChecks(hh *health.Handler, stores *StorageSet) {
	hh.Register("storage", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return stores.ReadyAll(ctx)
	})
	for _, name := range stores.Names() {
		n := name
		st, ok := stores.Get(n)
		if !ok {
			continue
		}
		hh.Register("storage:"+n, func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			return storage.Ready(ctx, st)
		})
	}
}
