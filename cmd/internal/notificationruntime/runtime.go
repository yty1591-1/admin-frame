// Package notificationruntime 装配通知域运行时（ENT-10）。
//
// 渠道驱动阶段语义（docs/notification-channels.md M4）：默认装配
// memory 驱动（只记录不出网），生产驱动（SMTP / 阿里云 / APNs / FCM）
// 由渠道配置就绪后经 NewWithRoute 注入；编排、可靠性、状态机与驱动无关。
package notificationruntime

import (
	"errors"
	"fmt"
	"os"

	"github.com/admin-frame/admin-frame/internal/notification/app"
	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/notification/infrastructure/channel"
	"github.com/admin-frame/admin-frame/internal/notification/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/notification/observability"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	pkgemail "github.com/admin-frame/admin-frame/pkg/email"
	pkgpush "github.com/admin-frame/admin-frame/pkg/push"
	"github.com/admin-frame/admin-frame/pkg/ratelimit"
	pkgsms "github.com/admin-frame/admin-frame/pkg/sms"
)

// EnvBlacklistSalt 是黑名单 HMAC 盐的环境变量名（ENT-07：密钥只走环境
// 注入；secrets.providers 就绪后迁移到托管源，逻辑名 notification_blacklist_salt）。
const EnvBlacklistSalt = "ADMIN_FRAME_NOTIFICATION_BLACKLIST_SALT"

// Runtime 是通知域运行时。
type Runtime struct {
	// Orchestrator 投递编排器（RegisterAsync 注册为消费 Handler）。
	Orchestrator *app.Orchestrator
	// Dispatcher 生产侧出口（业务域 / 代发 API 经它写 Outbox 事件）。
	Dispatcher *app.OutboxDispatcher
	// Templates 模板用例。
	Templates *app.TemplateService
	// Preferences 偏好用例。
	Preferences *app.PreferenceService
	// Blacklist 黑名单用例。
	Blacklist *app.BlacklistService
	// Replay 失败补偿用例。
	Replay *app.ReplayService
}

// New 构造通知运行时：仓储按 db.driver 切换 MySQL / memory，
// 渠道路由默认 memory 驱动（不出网，阶段语义见包注释），频控关闭。
func New(deps *platform.Deps) (*Runtime, error) {
	return NewWithRoute(deps, defaultMemoryRoute(), nil)
}

// NewWithRoute 构造通知运行时并注入渠道路由与频控。
//
// route 缺渠道时该渠道不可投（事件以 no address 语义被拒发，不会 panic）；
// limiter 可空（无频控）。
func NewWithRoute(deps *platform.Deps, route map[domain.Channel][]app.Sender, limiter *ratelimit.Keyed) (*Runtime, error) {
	if deps == nil || deps.Config == nil || deps.IDs == nil {
		return &Runtime{}, nil
	}
	repos, err := buildRepositories(deps)
	if err != nil {
		return nil, err
	}

	// 指标：deps.Metrics 缺失（最小进程形态）时退化为 noop——
	// 用例层已把 nil Observer 归一化，这里只为注册 Gauge 保留仓储引用。
	var observer app.Observer
	if deps.Metrics != nil {
		metrics, merr := observability.NewMetrics(deps.Metrics.RegistererWithoutChannel(), repos.deliveries.CountByStatus)
		if merr != nil {
			return nil, fmt.Errorf("notification metrics: %w", merr)
		}
		observer = metrics
	}
	emitter := buildEmitter(deps)

	opts := app.Options{Service: deps.Config.Service, Observer: observer}
	templates, err := app.NewTemplateService(repos.templates, deps.IDs, emitter, opts)
	if err != nil {
		return nil, fmt.Errorf("notification templates: %w", err)
	}
	preferences, err := app.NewPreferenceService(repos.preferences, deps.IDs, emitter, opts)
	if err != nil {
		return nil, fmt.Errorf("notification preferences: %w", err)
	}
	// 黑名单 HMAC 盐：只从环境注入（CFG-06），缺盐拒绝启动——
	// 无盐 HMAC 等于可离线枚举的普通哈希，等于没脱敏。
	hasher, err := blacklistHasher()
	if err != nil {
		return nil, err
	}
	blacklist, err := app.NewBlacklistService(repos.blacklist, hasher, deps.IDs, emitter, opts)
	if err != nil {
		return nil, fmt.Errorf("notification blacklist: %w", err)
	}
	orchestrator, err := app.NewOrchestrator(repos.requests, repos.deliveries, repos.templates,
		repos.preferences, repos.blacklist, app.OrchestratorOptions{
			Options: opts,
			Route:   route,
			Limiter: limiter,
			Emitter: emitter,
		})
	if err != nil {
		return nil, fmt.Errorf("notification orchestrator: %w", err)
	}
	if deps.Async == nil || deps.Async.Store() == nil {
		return nil, errors.New("notification runtime requires async infrastructure (async.enabled=true)")
	}
	dispatcher, err := app.NewOutboxDispatcher(deps.Async.Store())
	if err != nil {
		return nil, fmt.Errorf("notification dispatcher: %w", err)
	}
	replay, err := app.NewReplayService(repos.requests, dispatcher, repos.dlq, deps.IDs, emitter, opts)
	if err != nil {
		return nil, fmt.Errorf("notification replay: %w", err)
	}
	return &Runtime{
		Orchestrator: orchestrator,
		Dispatcher:   dispatcher,
		Templates:    templates,
		Preferences:  preferences,
		Blacklist:    blacklist,
		Replay:       replay,
	}, nil
}

// RegisterAsync 注册通知域异步 topic handler（worker 与 API 进程共用）。
func RegisterAsync(deps *platform.Deps, rt *Runtime) error {
	if deps == nil || deps.Async == nil || rt == nil || rt.Orchestrator == nil {
		return nil
	}
	if err := deps.Async.Register(app.TopicNotification, rt.Orchestrator.Handle); err != nil {
		return fmt.Errorf("notification async handler: %w", err)
	}
	return nil
}

// notificationRepos 聚合通知域仓储（requests/deliveries 共享同一 *sql.DB）。
type notificationRepos struct {
	requests    domain.RequestRepository
	deliveries  domain.DeliveryRepository
	templates   domain.TemplateRepository
	preferences domain.PreferenceRepository
	blacklist   domain.BlacklistRepository
	dlq         app.DLQReader
}

// buildRepositories 按 db.driver 构建仓储（memory 用于本地 / 单测）。
func buildRepositories(deps *platform.Deps) (notificationRepos, error) {
	if deps.Database == nil {
		return notificationRepos{
			requests:    persistence.NewMemoryRequestRepository(),
			deliveries:  persistence.NewMemoryDeliveryRepository(),
			templates:   persistence.NewMemoryTemplateRepository(),
			preferences: persistence.NewMemoryPreferenceRepository(),
			blacklist:   persistence.NewMemoryBlacklistRepository(),
			dlq:         persistence.NewMemoryDLQReader(),
		}, nil
	}
	sqlDB := deps.Database.SQL()
	requests, err := persistence.NewMySQLRequestRepository(sqlDB)
	if err != nil {
		return notificationRepos{}, fmt.Errorf("notification request repository: %w", err)
	}
	deliveries, err := persistence.NewMySQLDeliveryRepository(sqlDB)
	if err != nil {
		return notificationRepos{}, fmt.Errorf("notification delivery repository: %w", err)
	}
	templates, err := persistence.NewMySQLTemplateRepository(sqlDB)
	if err != nil {
		return notificationRepos{}, fmt.Errorf("notification template repository: %w", err)
	}
	preferences, err := persistence.NewMySQLPreferenceRepository(sqlDB)
	if err != nil {
		return notificationRepos{}, fmt.Errorf("notification preference repository: %w", err)
	}
	blacklist, err := persistence.NewMySQLBlacklistRepository(sqlDB)
	if err != nil {
		return notificationRepos{}, fmt.Errorf("notification blacklist repository: %w", err)
	}
	dlq, err := persistence.NewMySQLDLQReader(sqlDB)
	if err != nil {
		return notificationRepos{}, fmt.Errorf("notification dlq reader: %w", err)
	}
	return notificationRepos{
		requests: requests, deliveries: deliveries, templates: templates,
		preferences: preferences, blacklist: blacklist, dlq: dlq,
	}, nil
}

// buildEmitter 与租户域 buildDeps 同源：优先 Outbox（与业务事务同源），
// 退回双出口（审计表 + 结构化日志）。
func buildEmitter(deps *platform.Deps) audit.Emitter {
	slogEmitter := audit.SlogEmitter{Log: deps.Logger}
	if deps.Async != nil && deps.Async.Store() != nil {
		return audit.OutboxEmitter{Topic: "notification.audit", Events: deps.Async.Store()}
	}
	if deps.Database != nil {
		return audit.MultiEmitter{
			audit.SQLEmitter{DB: deps.Database.SQL()},
			slogEmitter,
		}
	}
	return slogEmitter
}

// blacklistHasher 从环境读取盐并构造 HMAC 函数。
func blacklistHasher() (app.TargetHasher, error) {
	salt := os.Getenv(EnvBlacklistSalt)
	if salt == "" {
		return nil, fmt.Errorf("notification blacklist salt: environment variable %s is required (empty = offline-enumerable hash)", EnvBlacklistSalt)
	}
	return app.HMACHasher([]byte(salt))
}

// defaultMemoryRoute 装配 memory 驱动路由（不出网，M4 阶段语义）。
// sms memory 驱动开启 AllowRawContent：通知模板在编排层渲染成成品正文，
// memory 驱动按裸内容记录（生产驱动走厂商模板 ID + 参数，见 SMSSender）。
func defaultMemoryRoute() map[domain.Channel][]app.Sender {
	return map[domain.Channel][]app.Sender{
		domain.ChannelEmail: {channel.NewEmailSender(pkgemail.NewMemory(pkgemail.MemoryConfig{}), pkgemail.Address{})},
		domain.ChannelSms:   {channel.NewSMSSender(pkgsms.NewMemory(pkgsms.MemoryConfig{AllowRawContent: true}), "", "")},
		domain.ChannelPush:  {channel.NewPushSender(pkgpush.NewMemory(pkgpush.MemoryConfig{}), pkgpush.PlatformIOS)},
	}
}
