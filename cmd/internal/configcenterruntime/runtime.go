// Package configcenterruntime 装配配置中心运行时产物（M1-3）。
//
// 装配语义与 featureflag / tenant runtime 一致：
//   - 内存 / MySQL 仓储按 db.driver 切换（MySQL 缺失时退化为进程内内存仓储）；
//   - 管理面（ItemService + ReleaseService + Handler）供 admin-api 注册；
//   - 启动时按代码 key spec 对每个 namespace 幂等播种 baseline Release；
//   - 审计事件与业务事务同源（Outbox），失败即启动失败（fail closed）。
package configcenterruntime

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/cmd/internal/snapshotconfig"
	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/configcenter/observability"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	configcenterrest "github.com/admin-frame/admin-frame/internal/configcenter/transport/rest"
	"github.com/admin-frame/admin-frame/internal/platform"
	platformaudit "github.com/admin-frame/admin-frame/internal/platform/audit"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/configclient"
	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/envelope"
	"github.com/admin-frame/admin-frame/pkg/hotconfig"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// 分页基线：与 A 端其它管理列表保持一致（page_size 上限 100）。
const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// 启动播种主体：系统身份，不冒用任何人类账号。
const (
	bootstrapSubject = "system:config-bootstrap"
	bootstrapTrace   = "config-bootstrap"
)

// 快照签名密钥通过部署注入（base64）：32 字节 seed 或 64 字节私钥。
// staging / prod 未配置时启动失败（fail closed），避免发布未签名快照。
const (
	signingKeyEnv       = "CONFIGCENTER_SNAPSHOT_SIGNING_KEY"
	signingKeyIDEnv     = "CONFIGCENTER_SNAPSHOT_SIGNING_KEY_ID"
	defaultSigningKeyID = "k1"
)

// Runtime 是配置中心域的运行时产物。
type Runtime struct {
	// Handler A 端管理 Handler；为 nil 表示本进程未装配（缺平台依赖）。
	Handler *configcenterrest.Handler
	// Snapshots 是当前生效快照读取服务，实现 configclient.Source；
	// 业务进程据此装配 pkg/configclient.Client。
	Snapshots *app.SnapshotService
	// Client 是进程内动态刷新客户端；nil 表示未配置公钥，动态刷新关闭。
	Client *configclient.Client
	// Metrics 是配置中心指标；deps.Metrics 缺失时为 nil（不观测）。
	Metrics *observability.Metrics
	// Hot 是进程级热更视图（ADR-0015）；未启用动态刷新时为 nil。
	//
	// 消费方应每次使用读当前视图，不要把取值缓存到局部变量。
	Hot *hotconfig.Holder
	// Secrets 是业务密钥用例（M2）；管理面 wrap / config-api unwrap。
	Secrets *app.SecretService

	// clientEnv / clientNamespaces 记录刷新归属，仅用于启动日志。
	clientEnv        string
	clientNamespaces []string
}

// Options 是装配参数。
type Options struct {
	// Service 审计服务名；空时取 deps.Config.Service。
	Service string
}

// repositories 汇总三个持久化端口；内存与 MySQL 实现同签名。
type repositories struct {
	items    domain.ItemRepository
	releases domain.ReleaseRepository
	pointers domain.PointerRepository
}

// New 装配配置中心运行时。
//
// 依赖缺失（deps / Config / IDs）返回装配错误，由调用方决定是否阻断本进程
// （admin-api 与 featureflag 同口径：记录告警并关闭本域路由）。
func New(deps *platform.Deps, opts Options) (*Runtime, error) {
	if deps == nil || deps.Config == nil {
		return nil, fmt.Errorf("configcenter runtime requires platform deps")
	}
	if deps.IDs == nil {
		return nil, fmt.Errorf("configcenter runtime requires id generator")
	}

	repos, sqlDB, err := buildRepositories(deps)
	if err != nil {
		return nil, err
	}
	emitter := buildEmitter(deps)
	service := opts.Service
	if service == "" {
		service = deps.Config.Service
	}

	// M1-4：平台启动键/热更键与 IAM 治理键已声明；业务域键待域 owner 补齐。
	registry, err := spec.Platform()
	if err != nil {
		return nil, fmt.Errorf("configcenter key spec: %w", err)
	}

	signer, err := buildSigner(deps.Config.Env, deps.Logger)
	if err != nil {
		return nil, err
	}
	metrics, err := buildMetrics(deps)
	if err != nil {
		return nil, err
	}
	appOpts := app.Options{Service: service, Signer: signer}

	itemService, err := app.NewItemService(repos.items, registry, emitter, deps.IDs, appOpts)
	if err != nil {
		return nil, fmt.Errorf("configcenter item service: %w", err)
	}
	releaseService, err := app.NewReleaseService(repos.items, repos.releases, repos.pointers,
		emitter, deps.IDs, appOpts)
	if err != nil {
		return nil, fmt.Errorf("configcenter release service: %w", err)
	}
	bootstrapService, err := app.NewBootstrapService(repos.items, repos.releases, repos.pointers,
		registry, emitter, deps.IDs, appOpts)
	if err != nil {
		return nil, fmt.Errorf("configcenter bootstrap service: %w", err)
	}
	if err := bootstrap(deps, bootstrapService, registry, sqlDB); err != nil {
		return nil, err
	}

	snapshots, err := app.NewSnapshotService(repos.items, repos.releases, repos.pointers)
	if err != nil {
		return nil, fmt.Errorf("configcenter snapshot service: %w", err)
	}

	secretRepos, err := buildSecretRepositories(sqlDB)
	if err != nil {
		return nil, err
	}
	kek, err := buildKEK(deps.Config.Env, deps.Logger)
	if err != nil {
		return nil, err
	}
	secretService, err := app.NewSecretService(
		secretRepos.Secrets, secretRepos.Grants, secretRepos.Access,
		kek, emitter, deps.IDs, appOpts)
	if err != nil {
		return nil, fmt.Errorf("configcenter secret service: %w", err)
	}

	handler, err := configcenterrest.NewHandler(configcenterrest.HandlerConfig{
		Items:    itemService,
		Releases: releaseService,
		Secrets:  secretService,
		Specs:    registry,
		PageLimits: pagination.Config{
			DefaultPageSize: defaultPageSize,
			MaxPageSize:     maxPageSize,
		},
		SQLDB: sqlDB,
	})
	if err != nil {
		return nil, fmt.Errorf("configcenter handler: %w", err)
	}

	// M1-7：管理面进程内动态刷新。未配置可信公钥时关闭刷新（继续使用
	// 启动期签名快照 / 文件配置）；配置了公钥则每 5s 拉取、验签并原子落盘。
	hot, err := newHotState()
	if err != nil {
		return nil, err
	}
	client, err := newRefreshClient(deps, snapshots, metrics, hot)
	if err != nil {
		return nil, err
	}
	if client == nil && deps.Logger != nil {
		deps.Logger.Warn("configcenter dynamic refresh disabled; set " + snapshotconfig.PublicKeysEnv)
	}
	startPendingApprovalGauge(deps, repos, registry, metrics)
	return &Runtime{
		Handler:          handler,
		Snapshots:        snapshots,
		Client:           client,
		Metrics:          metrics,
		Hot:              hotHolder(client, hot),
		Secrets:          secretService,
		clientEnv:        deps.Config.Env,
		clientNamespaces: snapshotconfig.EnabledNamespaces(),
	}, nil
}

// NewSecretResolve 装配仅含密钥解析的运行时（cmd/config-api）。
func NewSecretResolve(deps *platform.Deps, opts Options) (*Runtime, error) {
	if deps == nil || deps.Config == nil || deps.IDs == nil {
		return nil, fmt.Errorf("configcenter secret resolve requires platform deps")
	}
	emitter := buildEmitter(deps)
	service := opts.Service
	if service == "" {
		service = deps.Config.Service
	}
	kek, err := buildKEK(deps.Config.Env, deps.Logger)
	if err != nil {
		return nil, err
	}
	repos, sqlDB, err := buildRepositories(deps)
	if err != nil {
		return nil, err
	}
	secretRepos, err := buildSecretRepositories(sqlDB)
	if err != nil {
		return nil, err
	}
	secretService, err := app.NewSecretService(
		secretRepos.Secrets, secretRepos.Grants, secretRepos.Access,
		kek, emitter, deps.IDs, app.Options{Service: service})
	if err != nil {
		return nil, err
	}
	registry, err := spec.Platform()
	if err != nil {
		return nil, err
	}
	itemService, err := app.NewItemService(repos.items, registry, emitter, deps.IDs, app.Options{Service: service})
	if err != nil {
		return nil, err
	}
	releaseService, err := app.NewReleaseService(repos.items, repos.releases, repos.pointers,
		emitter, deps.IDs, app.Options{Service: service})
	if err != nil {
		return nil, err
	}
	handler, err := configcenterrest.NewHandler(configcenterrest.HandlerConfig{
		Items: itemService, Releases: releaseService, Secrets: secretService, Specs: registry,
		PageLimits: pagination.Config{DefaultPageSize: defaultPageSize, MaxPageSize: maxPageSize},
		SQLDB:      sqlDB,
	})
	if err != nil {
		return nil, err
	}
	return &Runtime{Handler: handler, Secrets: secretService}, nil
}

// buildKEK 按环境选择 LocalKEK 或 Vault Transit；prod 禁止 LocalKEK。
func buildKEK(env string, logger *slog.Logger) (envelope.KEKProvider, error) {
	driver := strings.TrimSpace(os.Getenv("CONFIGCENTER_KEK_DRIVER"))
	if driver == "" {
		driver = "local"
	}
	switch strings.ToLower(driver) {
	case "local":
		kek, err := envelope.NewLocalKEKFromEnv()
		if err != nil {
			return nil, fmt.Errorf("configcenter local kek: %w", err)
		}
		if err := envelope.RejectLocalKEKInProd(env, kek); err != nil {
			return nil, err
		}
		if logger != nil {
			logger.Info("configcenter kek provider", "driver", "local", "key_id", kek.KeyID())
		}
		return kek, nil
	case "vault", "vault_transit":
		kek, err := envelope.NewVaultTransit(
			os.Getenv("VAULT_ADDR"),
			os.Getenv("VAULT_TOKEN"),
			firstNonEmpty(os.Getenv("CONFIGCENTER_VAULT_TRANSIT_KEY"), "configcenter"),
		)
		if err != nil {
			return nil, fmt.Errorf("configcenter vault transit: %w", err)
		}
		if logger != nil {
			logger.Info("configcenter kek provider", "driver", "vault_transit", "key_id", kek.KeyID())
		}
		return kek, nil
	default:
		return nil, fmt.Errorf("configcenter unknown kek driver %q", driver)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// NewRefresh 只装配动态刷新客户端与客户端指标，不注册管理面、不播种。
//
// 供 biz-api / c-api / worker 在 MySQL 模式下观测配置中心可用性；内存模式
// 没有中心持久化数据，直接返回 (nil, nil)。刷新值目前不写回配置（热更需
// 先补 ADR 与白名单），这里只提供快照可用性、版本与 stale 指标。
func NewRefresh(deps *platform.Deps) (*Runtime, error) {
	if deps == nil || deps.Config == nil {
		return nil, fmt.Errorf("configcenter refresh runtime requires platform deps")
	}
	if deps.Database == nil {
		return nil, nil
	}
	repos, _, err := buildRepositories(deps)
	if err != nil {
		return nil, err
	}
	metrics, err := buildMetrics(deps)
	if err != nil {
		return nil, err
	}
	snapshots, err := app.NewSnapshotService(repos.items, repos.releases, repos.pointers)
	if err != nil {
		return nil, fmt.Errorf("configcenter snapshot service: %w", err)
	}
	hot, err := newHotState()
	if err != nil {
		return nil, err
	}
	client, err := newRefreshClient(deps, snapshots, metrics, hot)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, nil
	}
	return &Runtime{
		Snapshots:        snapshots,
		Client:           client,
		Metrics:          metrics,
		Hot:              hotHolder(client, hot),
		clientEnv:        deps.Config.Env,
		clientNamespaces: snapshotconfig.EnabledNamespaces(),
	}, nil
}

// newRefreshClient 构造按开关加载 namespace 的刷新客户端；未配置公钥返回 nil。
func newRefreshClient(deps *platform.Deps, source configclient.Source,
	metrics *observability.Metrics, hot *hotState) (*configclient.Client, error) {
	observer := clientRefreshObserver(deps.Logger, metrics)
	// 观测与热更共用同一个 OnRefresh：热更失败不影响观测，反之亦然。
	onRefresh := func(event configclient.RefreshEvent) {
		if observer != nil {
			observer(event)
		}
		hot.onEvent(event, deps.Logger, metrics, deps.LogLevel)
	}
	client, err := NewRefreshClient(RefreshConfig{
		Source:           source,
		PublicKeys:       os.Getenv(snapshotconfig.PublicKeysEnv),
		SnapshotFile:     os.Getenv(snapshotconfig.FileEnv),
		Env:              deps.Config.Env,
		Namespace:        spec.NamespacePlatform,
		Namespaces:       snapshotconfig.EnabledNamespaces(),
		PrimaryNamespace: spec.NamespacePlatform,
		OnRefresh:        onRefresh,
		OnWatchDropped: func() {
			if metrics != nil {
				metrics.ClientWatchDropped()
			}
		},
	})
	if err != nil {
		return nil, fmt.Errorf("configcenter refresh client: %w", err)
	}
	return client, nil
}

// buildSigner 从部署注入的密钥材料构造快照签名器。
//
// 未配置时：local / dev / test 允许关闭签名（客户端会拒绝未签名快照）；
// staging / prod 直接失败，避免生产发布无法被客户端验证的快照。
func buildSigner(env string, logger *slog.Logger) (*configclient.Signer, error) {
	raw := strings.TrimSpace(os.Getenv(signingKeyEnv))
	if raw == "" {
		if env == "staging" || env == "prod" {
			return nil, fmt.Errorf("configcenter: %s is required when env=%s", signingKeyEnv, env)
		}
		if logger != nil {
			logger.Warn("configcenter snapshot signing disabled; snapshots will be rejected by clients",
				"env", env, "key_env", signingKeyEnv)
		}
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("configcenter: %s is not valid base64", signingKeyEnv)
	}
	var privateKey ed25519.PrivateKey
	switch len(decoded) {
	case ed25519.SeedSize:
		privateKey = ed25519.NewKeyFromSeed(decoded)
	case ed25519.PrivateKeySize:
		privateKey = append(ed25519.PrivateKey(nil), decoded...)
	default:
		return nil, fmt.Errorf("configcenter: %s must be a base64 ed25519 seed or private key", signingKeyEnv)
	}
	keyID := strings.TrimSpace(os.Getenv(signingKeyIDEnv))
	if keyID == "" {
		keyID = defaultSigningKeyID
	}
	return configclient.NewSigner(keyID, privateKey)
}

// bootstrap 对每个已声明 namespace 幂等播种 baseline；任一失败即返回（fail fast）。
//
// 每个 namespace 单独一个事务：一个 namespace 失败不会留下半状态，也不会
// 回滚其它已成功的 namespace（播种本身幂等，重启可继续）。
func bootstrap(deps *platform.Deps, service *app.BootstrapService,
	registry *spec.Registry, sqlDB *sql.DB) error {
	bootstrapActor := app.Actor{
		SubjectID: bootstrapSubject,
		ActorType: "system",
		Channel:   "admin",
		TraceID:   bootstrapTrace,
	}
	for _, namespace := range registry.Namespaces() {
		if err := withinTx(context.Background(), sqlDB, func(ctx context.Context) error {
			_, ensureErr := service.EnsureBaseline(ctx, app.BootstrapInput{
				Env:       deps.Config.Env,
				Namespace: namespace,
				Actor:     bootstrapActor,
			})
			return ensureErr
		}); err != nil {
			return fmt.Errorf("configcenter bootstrap namespace %q: %w", namespace, err)
		}
	}
	return nil
}

// withinTx 在 SQL 模式下包事务；内存模式直接执行。
func withinTx(ctx context.Context, sqlDB *sql.DB, fn func(context.Context) error) error {
	if sqlDB == nil {
		return fn(ctx)
	}
	return db.WithinUnitOfWork(ctx, sqlDB, nil, fn)
}

// buildRepositories 内存 / MySQL 切换（对齐 tenantruntime 口径）。
func buildRepositories(deps *platform.Deps) (repositories, *sql.DB, error) {
	if deps.Database == nil {
		memoryRepos := memory.New()
		return repositories{
			items:    memoryRepos.Items,
			releases: memoryRepos.Releases,
			pointers: memoryRepos.Pointers,
		}, nil, nil
	}
	sqlDB := deps.Database.SQL()
	repos, err := persistence.New(sqlDB)
	if err != nil {
		return repositories{}, nil, fmt.Errorf("configcenter repository: %w", err)
	}
	return repositories{
		items:    repos.Items,
		releases: repos.Releases,
		pointers: repos.Pointers,
	}, sqlDB, nil
}

type secretRepos struct {
	Secrets domain.SecretRepository
	Grants  domain.SecretGrantRepository
	Access  domain.SecretAccessRepository
}

func buildSecretRepositories(sqlDB *sql.DB) (secretRepos, error) {
	if sqlDB == nil {
		mem := memory.NewSecrets()
		return secretRepos{Secrets: mem.Secrets, Grants: mem.Grants, Access: mem.Access}, nil
	}
	repos, err := persistence.NewSecrets(sqlDB)
	if err != nil {
		return secretRepos{}, err
	}
	return secretRepos{Secrets: repos.Secrets, Grants: repos.Grants, Access: repos.Access}, nil
}

// buildMetrics 构造配置中心指标；进程未装配指标时不观测（测试形态）。
func buildMetrics(deps *platform.Deps) (*observability.Metrics, error) {
	if deps.Metrics == nil {
		return nil, nil
	}
	metrics, err := observability.New(deps.Metrics.Registerer())
	if err != nil {
		return nil, fmt.Errorf("configcenter metrics: %w", err)
	}
	return metrics, nil
}

// startPendingApprovalGauge 周期刷新待审批数量（M1 审批积压告警）。
//
// 只在装配了后台任务的进程注册；查询失败保留上一次值，不阻断主流程。
func startPendingApprovalGauge(deps *platform.Deps, repos repositories,
	registry *spec.Registry, metrics *observability.Metrics) {
	if deps.Background == nil || metrics == nil {
		return
	}
	env := deps.Config.Env
	namespaces := registry.Namespaces()
	deps.Background(func(ctx context.Context) error {
		refresh := func() {
			for _, namespace := range namespaces {
				_, total, err := repos.releases.List(ctx, env, namespace,
					domain.ReleaseStatusPendingReview, 1, 0)
				if err != nil {
					continue
				}
				metrics.SetPendingApproval(namespace, total)
			}
		}
		refresh()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				refresh()
			}
		}
	})
}

// buildEmitter 审计出口：优先 Outbox（与业务事务同源），退回双出口
// （审计表 + 结构化日志），内存模式退回结构化日志。
func buildEmitter(deps *platform.Deps) audit.Emitter {
	slogEmitter := platformaudit.SlogEmitter{Log: deps.Logger}
	if deps.Async != nil && deps.Async.Store() != nil {
		return platformaudit.OutboxEmitter{Topic: "configcenter.audit", Events: deps.Async.Store()}
	}
	if deps.Database != nil {
		return platformaudit.MultiEmitter{
			platformaudit.SQLEmitter{DB: deps.Database.SQL()},
			slogEmitter,
		}
	}
	return slogEmitter
}

// hotHolder 在启用动态刷新时返回热更视图持有者；客户端未启用时返回 nil，
// 避免调用方误以为存在可读视图。
func hotHolder(client *configclient.Client, hot *hotState) *hotconfig.Holder {
	if client == nil || hot == nil {
		return nil
	}
	return hot.holder
}
