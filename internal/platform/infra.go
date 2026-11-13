package platform

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/internal/platform/dependency"
	dependencyobservability "github.com/admin-frame/admin-frame/internal/platform/dependency/observability"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
	"github.com/admin-frame/admin-frame/internal/platform/lifecycle"
	platformobservability "github.com/admin-frame/admin-frame/internal/platform/observability"
	platformstorage "github.com/admin-frame/admin-frame/internal/platform/storage"

	// 注册 MySQL 与 PostgreSQL 的 database/sql 驱动（迁移与业务共用同一驱动集合）。
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"go.opentelemetry.io/otel/trace"

	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/health"
	pkghttpx "github.com/admin-frame/admin-frame/pkg/httpx"
	"github.com/admin-frame/admin-frame/pkg/metrics"
	"github.com/admin-frame/admin-frame/pkg/redis"
	"github.com/admin-frame/admin-frame/pkg/tracing"
)

// infraState 是 prepareInfra 产出的进程级基础设施。
type infraState struct {
	cfg        *platformconfig.Config
	logger     *slog.Logger
	logLevel   *slog.LevelVar
	nodeID     int64
	build      httpserver.BuildInfo
	metrics    *metrics.Metrics
	tracer     *tracing.Provider
	stores     *platformstorage.Set
	database   *db.DB
	redis      *redis.Client
	health     *health.Handler
	clientIP   *pkghttpx.ClientIPResolver
	background *lifecycle.Tasks
	deps       *Deps
}

// prepareInfra 构造进程级基础设施（配置、日志、指标、追踪、存储、数据库、
// Redis、健康检查、依赖注册表），并建立 deps 骨架。
//
// 任一阶段失败时已建资源由 infraState.close 回收，主错误优先。
func prepareInfra(ctx context.Context, opts Options) (infra *infraState, err error) {
	st := &infraState{background: lifecycle.NewTasks()}
	infra = st
	deferredCleanup := func() {
		if err == nil {
			return
		}
		// 失败路径回收已建资源；主错误优先，清理错误不再覆盖（与历史行为一致）。
		_ = st.close()
	}
	defer deferredCleanup()

	cfg := opts.Config
	if cfg == nil {
		loaded, loadErr := platformconfig.LoadConfig(opts.ConfigPath)
		if loadErr != nil {
			return nil, fmt.Errorf("load config: %w", loadErr)
		}
		cfg = loaded
	}
	infra.cfg = cfg
	logger, logLevel := platformobservability.NewLoggerWithLevel(cfg)
	infra.logger = logger
	infra.logLevel = logLevel

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

	stores, err := platformstorage.OpenStorageSet(ctx, cfg.Storage)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	infra.stores = stores

	database, err := OpenDatabase(ctx, cfg.DB, tp.TracerProvider())
	if err != nil {
		return nil, err
	}
	infra.database = database
	if err := platformobservability.RegisterDBMetrics(met.RegistererWithoutChannel(), database); err != nil {
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
		LogLevel:   logLevel,
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

// close 按资源注册逆序释放，返回首个失败。
//
// 返回 error 而非写 *error：调用方决定「主错误优先」还是「以清理错误为准」，
// 避免把错误合并策略藏在清理函数里。
func (s *infraState) close() error {
	var first error
	record := func(closeErr error) {
		if closeErr != nil && first == nil {
			first = closeErr
		}
	}
	if s.redis != nil {
		if closeErr := s.redis.Close(); closeErr != nil {
			record(fmt.Errorf("redis close: %w", closeErr))
		}
	}
	if s.database != nil {
		if closeErr := s.database.Close(); closeErr != nil {
			record(fmt.Errorf("db close: %w", closeErr))
		}
	}
	if s.stores != nil {
		if closeErr := s.stores.Close(); closeErr != nil {
			record(fmt.Errorf("storage close: %w", closeErr))
		}
	}
	if s.tracer != nil {
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if closeErr := s.tracer.Shutdown(shCtx); closeErr != nil {
			record(fmt.Errorf("tracing shutdown: %w", closeErr))
		}
	}
	return first
}

// sqlDriverName 把配置中的引擎名映射为已注册的 database/sql 驱动名。
//
// 配置面向运维使用 postgres，而 pgx stdlib 注册名是 pgx，必须在此显式转换；
// 未知引擎直接失败，避免静默回落到其它数据库。
func sqlDriverName(driver string) (string, error) {
	switch driver {
	case "mysql":
		return "mysql", nil
	case "postgres", "postgresql", "pgx":
		return "pgx", nil
	default:
		return "", fmt.Errorf("platform: unsupported db.driver %q", driver)
	}
}

// dbConfigFrom 把配置映射为 pkg/db 的连接参数（纯函数，便于单测覆盖字段搬运）。
//
// 池参数原样传递：0 交由 pkg/db.applyDefaults 填充，避免在两处维护默认值。
func dbConfigFrom(cfg platformconfig.DBConfig) (db.Config, error) {
	driver, err := sqlDriverName(cfg.Driver)
	if err != nil {
		return db.Config{}, err
	}
	return db.Config{
		Driver:          driver,
		DSN:             cfg.DSN,
		MaxOpenConns:    cfg.MaxOpenConns,
		MaxIdleConns:    cfg.MaxIdleConns,
		ConnMaxLifetime: cfg.ConnMaxLifetime,
	}, nil
}

// OpenDatabase 按配置打开进程数据库；driver=memory 返回 nil（业务使用内存实现）。
//
// 连接池参数取自 db.max_open_conns / max_idle_conns / conn_max_lifetime，未配置时由
// pkg/db 填充默认值；tp 非 nil 时为 SQL 调用创建 CLIENT span（调用方传本进程的
// TracerProvider，worker 等未接入 tracing 的进程传 nil）。
// 调用方须在使用完毕后 Close。
func OpenDatabase(ctx context.Context, cfg platformconfig.DBConfig, tp trace.TracerProvider) (*db.DB, error) {
	if cfg.Driver == "" || cfg.Driver == "memory" {
		return nil, nil
	}
	dbCfg, err := dbConfigFrom(cfg)
	if err != nil {
		return nil, err
	}
	database, err := db.OpenTraced(ctx, dbCfg, tp)
	if err != nil {
		return nil, fmt.Errorf("db: %w", err)
	}
	return database, nil
}

// OpenRedis 按配置打开共享 Redis；未配置 addr 时返回 nil（单实例 / 本地）。
//
// 连接失败即返回错误：IAM enforce 依赖 Redis 时不得静默降级为单层内存缓存。
func OpenRedis(ctx context.Context, cfg platformconfig.RedisConfig) (*redis.Client, error) {
	if !cfg.Enabled() {
		return nil, nil
	}
	client, err := redis.Open(ctx, redis.Config{
		Addr:      cfg.Addr,
		Username:  cfg.Username,
		Password:  cfg.Password,
		DB:        cfg.DB,
		KeyPrefix: cfg.KeyPrefix,
	})
	if err != nil {
		return nil, fmt.Errorf("redis: %w", err)
	}
	return client, nil
}
