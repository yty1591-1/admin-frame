package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform/async"
	platformauthn "github.com/admin-frame/admin-frame/internal/platform/authn"
	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/internal/platform/dependency"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
	"github.com/admin-frame/admin-frame/internal/platform/lifecycle"
	platformstorage "github.com/admin-frame/admin-frame/internal/platform/storage"
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
	Config *platformconfig.Config
	Logger *slog.Logger
	// LogLevel 是日志级别的运行期开关；非 nil 时可由配置热更直接 Set（ADR-0015）。
	LogLevel *slog.LevelVar
	Metrics  *metrics.Metrics
	Tracer   *tracing.Provider
	Storage  *platformstorage.Set
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
	Async *async.Runtime

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
	// Config 是组合加载层已构建的完整配置（平台段 + 业务域）。
	//
	// 非空时不再读取 ConfigPath；cmd 通过 cmd/internal/appconfig 加载后传入，
	// 平台只持有平台段，域配置由 cmd 闭包自行传递给各域 runtime。
	Config *platformconfig.Config
	// Build 是构建身份；Service 为空时取配置中的 service。
	Build httpserver.BuildInfo
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
	// 清理错误不得覆盖启动主错误。
	defer func() {
		if closeErr := infra.close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	// 步骤 2：异步运行时。必须先于身份运行时：身份域审计写入依赖 deps.Async。
	if err := buildAsync(infra.deps); err != nil {
		return err
	}
	// 步骤 3：鉴权守卫（租户状态 / 配额）。必须先于身份运行时，使 cmd 在构造
	// 身份运行时之前拿到租户判定绑定（经 Options.NewAuthGuards 返回值传递）。
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
// 平台只保存守卫链本身；租户判定等业务语义绑定由 cmd 在 NewAuthGuards 内
// 自行保存并显式传给域运行时，不写回 Deps。
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
	authMW, err := platformauthn.NewAuthMiddleware(cfg.Auth, auth.Channel(cfg.Channel), cfg.Env, authOpts...)
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
func buildRouter(deps *Deps, opts Options, build httpserver.BuildInfo) (http.Handler, error) {
	cfg := deps.Config
	if cfg == nil {
		return nil, errors.New("platform: buildRouter requires config")
	}
	api := http.NewServeMux()
	if err := opts.Register(deps, api); err != nil {
		return nil, fmt.Errorf("register routes: %w", err)
	}

	root := http.NewServeMux()
	httpserver.MountHealth(root, deps.Health)
	httpserver.MountVersion(root, build)
	httpserver.MountMetrics(root, deps.Metrics.Handler())
	root.Handle("/", api)

	pipeline, err := httpserver.NewPipeline(httpserver.PipelineConfig{
		Timeout:        cfg.HTTP.Timeout,
		BodyLimitBytes: cfg.HTTP.BodyLimitBytes,
		Channel:        cfg.Channel,
		RateLimit:      cfg.RateLimit,
		Idempotency:    cfg.Idempotency,
		CORS:           cfg.CORS,
	}, httpserver.PipelineDeps{
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
	return httpserver.ListenAndServe(ctx, infra.cfg, handler, infra.logger)
}

// registerStorageChecks 为默认桶与命名桶注册就绪探测。
func registerStorageChecks(hh *health.Handler, stores *platformstorage.Set) {
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

// NewAsyncRuntime 把根包 Deps 转译为子包局部依赖，避免 async 反向依赖平台根包。
func NewAsyncRuntime(deps *Deps) (*async.Runtime, error) {
	if deps == nil {
		return nil, fmt.Errorf("async runtime: deps is required")
	}
	return async.NewRuntime(async.RuntimeDeps{
		Config: deps.Config, Logger: deps.Logger, Database: deps.Database, Metrics: deps.Metrics,
	})
}
