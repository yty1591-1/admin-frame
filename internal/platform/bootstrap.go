package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform/dependency"
	dependencyobservability "github.com/admin-frame/admin-frame/internal/platform/dependency/observability"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
	"github.com/admin-frame/admin-frame/internal/platform/lifecycle"
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
	// VerifyTenant 判定商户当前是否可服务（ENT-09），由 cmd 装配期写入；
	// nil 表示本端不做租户校验。平台只保存通用函数签名，不定义业务语义端口。
	VerifyTenant func(ctx context.Context, merchantID string) (bool, error)
	// IsTenantMember 判定主体是否为租户在册成员（ENT-09 T-22）；
	// nil 表示本端不提供成员关系查询（切换端点将拒绝服务）。
	IsTenantMember func(ctx context.Context, tenantID, subjectID string) (bool, error)
	// Dependencies 是外部依赖注册表（ENT-13）：出站依赖（第三方 HTTP API
	// 等）在装配期按名登记超时 / 重试 / 熔断，运行期经 Caller.Do 执行并
	// 出指标。nil 仅出现在单测形态。
	Dependencies *dependency.Registry
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

	// authGuards 是 buildGuards 产出的鉴权链守卫（按声明顺序）；
	// 由 buildAuthN 读取并插入 AuthMW 内层。不导出：顺序只由 Run 编排。
	authGuards []func(http.Handler) http.Handler
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
	// NewAuthGuards 构造鉴权链守卫（租户状态 / API 配额等）；返回 nil 表示不启用。
	//
	// 在 AuthMW 组装前调用，返回值按数组顺序插入 AuthMW 内层：
	// auth → guards[0] → guards[1] → handler。顺序即显式装配契约，
	// 不再由平台层推断（已停用租户应先于配额判定被拒绝）。
	NewAuthGuards func(*Deps) ([]func(http.Handler) http.Handler, error)
	// NewAuthorizer 构造权限校验器；返回 nil 或 nil 函数时默认拒绝所有权限点（fail-closed）。
	//
	// 参数为已装配的平台能力（配置 / 数据库 / Redis / 指标 / 健康检查），
	// 使 cmd 可在同一处构造 IAM 快照加载器并复用其仓储与缓存。
	NewAuthorizer func(*Deps) (*security.Authorizer, error)
	// Register 注册本端 transport 与业务依赖；不得注册他端路由。
	Register func(*Deps, *http.ServeMux) error
}

// infraState 是 prepareInfra 产出的进程级基础设施。
type infraState struct {
	cfg        *Config
	logger     *slog.Logger
	nodeID     int64
	build      BuildInfo
	metrics    *metrics.Metrics
	tracer     *tracing.Provider
	stores     *StorageSet
	database   *db.DB
	redis      *redis.Client
	health     *health.Handler
	clientIP   *pkghttpx.ClientIPResolver
	background *lifecycle.Tasks
	deps       *Deps
}

// Run 完成一次完整的进程装配与运行：配置 → 日志 → 指标 → 追踪 → 存储 →
// 鉴权 → 请求链路 → 探针 → 监听 → 优雅关闭 → 资源释放。
//
// 任一阶段失败返回 error，由 cmd 决定退出码；本函数不使用 log.Fatal / os.Exit，
// 保证 defer 清理一定执行（REL-02）。
//
// 步骤顺序即装配契约：每步只依赖前序步骤写入 infraState / Deps 的能力，
// 顺序变更会改变运行期语义（见各步骤注释）。
func Run(ctx context.Context, opts Options) (err error) {
	if opts.Register == nil {
		return errors.New("platform: Options.Register is required")
	}

	// 步骤 1：基础设施。失败时内部已完成的部分由 infraState.close 回收。
	infra, err := prepareInfra(ctx, opts)
	if err != nil {
		return err
	}
	defer infra.close(&err)

	// 步骤 2：异步运行时。必须先于身份运行时：身份域审计写入依赖 deps.Async。
	if err := buildAsync(infra.deps); err != nil {
		return err
	}
	// 步骤 3：鉴权守卫（租户状态 / 配额）。必须先于身份运行时：登录 / 开通需要读到
	// deps.VerifyTenant。
	if err := buildGuards(infra.deps, opts); err != nil {
		return err
	}
	// 步骤 4：鉴权链。撤销检查必须先于 AuthMW；守卫按声明顺序包在 AuthMW 内层。
	if err := buildAuthN(infra.deps, opts); err != nil {
		return err
	}
	// 步骤 5：权限校验器。
	if err := buildAuthorizer(infra.deps, opts); err != nil {
		return err
	}
	// 步骤 6：路由与请求链路。
	handler, err := buildRouter(infra.deps, opts, infra.build)
	if err != nil {
		return err
	}
	// 步骤 7：启动后台任务并监听，直到 ctx 取消或收到退出信号。
	return serve(ctx, infra, handler)
}

// prepareInfra 构造进程级基础设施（配置、日志、指标、追踪、存储、数据库、
// Redis、健康检查、依赖注册表），并建立 deps 骨架。
func prepareInfra(ctx context.Context, opts Options) (infra *infraState, err error) {
	st := &infraState{background: lifecycle.NewTasks()}
	infra = st
	deferredCleanup := func() {
		if err != nil {
			st.close(&err)
		}
	}
	defer deferredCleanup()

	cfg, err := LoadConfig(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	infra.cfg = cfg
	logger := NewLogger(cfg)
	infra.logger = logger

	ids, nodeID, err := NewIDGenerator(cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("id generator: %w", err)
	}
	infra.nodeID = nodeID

	met, err := metrics.New(metrics.Options{
		Service: cfg.Service,
		Channel: cfg.Channel,
		Env:     cfg.Env,
	})
	if err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	infra.metrics = met
	build := opts.Build
	if build.Service == "" {
		build.Service = cfg.Service
	}
	infra.build = build
	if err := met.RegisterBuildInfo(build.Version, build.Commit, build.BuildTime); err != nil {
		return nil, fmt.Errorf("metrics build info: %w", err)
	}

	tp, err := tracing.Init(ctx, cfg.TracingOptions(build.Version))
	if err != nil {
		return nil, fmt.Errorf("tracing: %w", err)
	}
	infra.tracer = tp

	stores, err := OpenStorageSet(ctx, cfg.Storage)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	infra.stores = stores

	database, err := OpenDatabase(ctx, cfg.DB, tp.TracerProvider())
	if err != nil {
		return nil, err
	}
	infra.database = database
	if err := RegisterDBMetrics(met.RegistererWithoutChannel(), database); err != nil {
		return nil, fmt.Errorf("db metrics: %w", err)
	}

	redisClient, err := OpenRedis(ctx, cfg.Redis)
	if err != nil {
		return nil, err
	}
	infra.redis = redisClient

	hh := health.New()
	registerStorageChecks(hh, stores)
	if database != nil {
		hh.Register("db", database.Checker())
	}
	if redisClient != nil {
		hh.Register("redis", redisClient.Checker())
	}
	infra.health = hh

	clientIPResolver, err := pkghttpx.NewClientIPResolver(cfg.HTTP.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("trusted proxies: %w", err)
	}
	infra.clientIP = clientIPResolver

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
		Background: infra.background.Add,
	}
	// ENT-13：外部依赖注册表——出站依赖统一登记（超时 / 重试 / 熔断 +
	// dependency_* 指标）；指标缺失时 Observer 为 noop（app 层归一）。
	var depObserver dependency.Observer
	if met != nil {
		obs, obsErr := dependencyobservability.NewMetrics(met.RegistererWithoutChannel())
		if obsErr != nil {
			return nil, fmt.Errorf("dependency observability: %w", obsErr)
		}
		depObserver = obs
	}
	deps.Dependencies = dependency.NewRegistry(depObserver)
	infra.deps = deps

	return infra, nil
}

// close 按资源注册逆序释放；错误只在首个失败时写入 err，避免覆盖主错误。
func (s *infraState) close(err *error) {
	if s.redis != nil {
		if closeErr := s.redis.Close(); closeErr != nil && *err == nil {
			*err = fmt.Errorf("redis close: %w", closeErr)
		}
	}
	if s.database != nil {
		if closeErr := s.database.Close(); closeErr != nil && *err == nil {
			*err = fmt.Errorf("db close: %w", closeErr)
		}
	}
	if s.stores != nil {
		if closeErr := s.stores.Close(); closeErr != nil && *err == nil {
			*err = fmt.Errorf("storage close: %w", closeErr)
		}
	}
	if s.tracer != nil {
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if closeErr := s.tracer.Shutdown(shCtx); closeErr != nil && *err == nil {
			*err = fmt.Errorf("tracing shutdown: %w", closeErr)
		}
	}
}

// buildAsync 构造通用异步运行时；必须先于身份运行时装配。
func buildAsync(deps *Deps) error {
	if deps.Config == nil {
		return errors.New("platform: buildAsync requires config")
	}
	asyncRuntime, err := NewAsyncRuntime(deps)
	if err != nil {
		return fmt.Errorf("async runtime: %w", err)
	}
	deps.Async = asyncRuntime
	if asyncRuntime != nil && asyncRuntime.PublishInAPI() {
		deps.Background(asyncRuntime.Run)
	}
	return nil
}

// buildGuards 构造鉴权链守卫（租户状态 / API 配额等）；必须先于身份运行时装配。
//
// 守卫构造期会写入 deps.VerifyTenant 等通用函数字段，供 identityruntime 在
// NewSessionValidator 阶段读取（登录 / 开通据此校验租户是否在服务期）。
func buildGuards(deps *Deps, opts Options) error {
	if deps.Config == nil {
		return errors.New("platform: buildGuards requires config")
	}
	if opts.NewAuthGuards == nil {
		return nil
	}
	guards, err := opts.NewAuthGuards(deps)
	if err != nil {
		return fmt.Errorf("auth guards: %w", err)
	}
	deps.authGuards = guards
	return nil
}

// buildAuthN 组装鉴权中间件：撤销检查 → AuthMW → 守卫链（顺序由 buildGuards 决定）。
//
// 撤销检查必须在 AuthMW 前注入（docs/identity-authn.md §13.2）；
// 守卫包在 AuthMW 内层：鉴权先写入 Principal，守卫再读 merchant_id。
func buildAuthN(deps *Deps, opts Options) error {
	if deps.Config == nil {
		return errors.New("platform: buildAuthN requires config")
	}
	if deps.Logger == nil {
		return errors.New("platform: buildAuthN requires logger")
	}
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

	cfg := deps.Config
	authMW, err := NewAuthMiddleware(cfg.Auth, auth.Channel(cfg.Channel), cfg.Env, authOpts...)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	// 守卫顺序即装配声明：guards[0]（租户状态）先于 guards[1]（配额）执行。
	deps.AuthMW = httpserver.AfterAuth(authMW, deps.authGuards...)
	return nil
}

// buildAuthorizer 构造权限校验器；未注入时默认拒绝所有权限点（fail-closed）。
func buildAuthorizer(deps *Deps, opts Options) error {
	if deps.Config == nil {
		return errors.New("platform: buildAuthorizer requires config")
	}
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
	return nil
}

// buildRouter 注册业务路由、探针端点与入口请求链路。
func buildRouter(deps *Deps, opts Options, build BuildInfo) (http.Handler, error) {
	cfg := deps.Config
	if cfg == nil {
		return nil, errors.New("platform: buildRouter requires config")
	}
	api := http.NewServeMux()
	if err := opts.Register(deps, api); err != nil {
		return nil, fmt.Errorf("register routes: %w", err)
	}

	root := http.NewServeMux()
	MountHealth(root, deps.Health)
	MountVersion(root, build)
	MountMetrics(root, deps.Metrics.Handler())
	root.Handle("/", api)

	pipeline, err := NewPipeline(PipelineConfig{
		Timeout:        cfg.HTTP.Timeout,
		BodyLimitBytes: cfg.HTTP.BodyLimitBytes,
		Channel:        cfg.Channel,
		RateLimit:      cfg.RateLimit,
		Idempotency:    cfg.Idempotency,
		CORS:           cfg.CORS,
	}, PipelineDeps{
		Logger:   deps.Logger,
		Metrics:  deps.Metrics,
		Tracer:   deps.Tracer,
		Redis:    deps.Redis,
		ClientIP: deps.ClientIP,
	})
	if err != nil {
		return nil, err
	}
	return pipeline(root), nil
}

// serve 启动后台任务并监听，直到 ctx 取消或收到退出信号。
func serve(ctx context.Context, infra *infraState, handler http.Handler) error {
	tasks := infra.background.List()
	stopBackground := lifecycle.Start(ctx, tasks, infra.logger)
	defer stopBackground()

	infra.logger.Info("process starting",
		"service", infra.cfg.Service,
		"channel", infra.cfg.Channel,
		"env", infra.cfg.Env,
		"version", infra.build.Version,
		"node_id", infra.nodeID,
		"background_tasks", len(tasks),
	)
	return ListenAndServe(ctx, infra.cfg, handler, infra.logger)
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
