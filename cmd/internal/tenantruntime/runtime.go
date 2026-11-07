// Package tenantruntime 装配租户域（ENT-09）。
//
// 装配职责：按数据库可用性选择仓储实现，注入审计出口与 ID 生成器，
// 构造 A 端 Handler 与 B 端状态守卫。租户域没有配置开关：它是隔离边界，
// 缺它则 B 端无法回答「这个商户是否还在服务期」。
package tenantruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	iamruntime "github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	iamapp "github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/platform"
	platformaudit "github.com/admin-frame/admin-frame/internal/platform/audit"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	tenantconfig "github.com/admin-frame/admin-frame/internal/tenant/config"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
	tenantobservability "github.com/admin-frame/admin-frame/internal/tenant/observability"
	tenantrest "github.com/admin-frame/admin-frame/internal/tenant/transport/rest"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// 分页基线：与 A 端其它管理列表保持一致（page_size 上限 100）。
const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// Runtime 是租户域的运行时产物。
type Runtime struct {
	// Handler A 端租户管理 Handler；为 nil 表示本进程未启用（缺配置或依赖）。
	Handler *tenantrest.Handler
	// invitationService 保留引用：ENT-10 通知装配晚于本构造时，
	// 经 SetInvitationNotifier 后补注入（服务指针与 Handler 共享）。
	invitationService *app.InvitationService
}

// SetInvitationNotifier 注入邀请邮件投递端口（ENT-10）。
// 必须在 Handler 对外服务前调用（装配期一次）；nil 值忽略（保持只落库语义）。
func (r *Runtime) SetInvitationNotifier(notifier app.InvitationNotifier) {
	if r == nil || r.invitationService == nil || notifier == nil {
		return
	}
	r.invitationService.SetNotifier(notifier)
}

// SetFeatureEvaluator 注入特性开关消费端口（ENT-11：邀请邮件灰度开关）。
// 装配期一次；nil 值忽略（保持 ENT-10 语义）。
func (r *Runtime) SetFeatureEvaluator(evaluator app.FeatureEvaluator) {
	if r == nil || r.invitationService == nil || evaluator == nil {
		return
	}
	r.invitationService.SetFeatureEvaluator(evaluator)
}

// New 构造租户运行时（A 端管理能力）。
func New(deps *platform.Deps, revoker app.SessionRevoker, tenantCfg tenantconfig.TenantConfig) (*Runtime, error) {
	if deps == nil || deps.Config == nil || deps.IDs == nil {
		return &Runtime{}, nil
	}

	store, emitter, err := buildDeps(deps)
	if err != nil {
		return nil, err
	}
	repo := store.tenants

	// T-16：租户域指标。deps.Metrics 缺失（最小进程形态）时退化为 noop——
	// 用例层已把 nil Observer 归一化，这里只为注册 Gauge 保留引用。
	var tenantMetrics *tenantobservability.Metrics
	if deps.Metrics != nil {
		tenantMetrics, err = tenantobservability.NewMetrics(deps.Metrics.RegistererWithoutChannel())
		if err != nil {
			return nil, fmt.Errorf("tenant metrics: %w", err)
		}
		if err := tenantMetrics.RegisterStatusGauge(repo.CountByStatus); err != nil {
			return nil, fmt.Errorf("tenant metrics: %w", err)
		}
	}
	var observer app.Observer
	if tenantMetrics != nil {
		observer = tenantMetrics
	}

	// T-18（ADR-0007）：成员角色 → IAM 绑定同步。IAM 仓储与租户共用同一
	// *sql.DB（或同为内存），绑定写入复用租户域事务，失败整体回滚。
	roleSyncer, err := newRoleSyncerFromDeps(deps)
	if err != nil {
		return nil, err
	}

	// T-22：成员关系查询注入 Deps（identity 的切换端点据此校验成员关系）。
	// New 自身也会被 worker 等路径单独调用，这里幂等注入。
	if err := InjectMembership(deps); err != nil {
		return nil, err
	}

	service, err := app.NewService(repo, deps.IDs, emitter, app.Options{
		DefaultPageSize: defaultPageSize,
		MaxPageSize:     maxPageSize,
		Service:         deps.Config.Service,
		Revoker:         revoker,
		Plans:           store.plans,
		Members:         store.members,
		Quotas:          store.quotas,
		Approvals:       store.approvals,
		Observer:        observer,
		RoleSyncer:      roleSyncer,
	})
	if err != nil {
		return nil, fmt.Errorf("tenant service: %w", err)
	}

	// 成员用例必须拿到配额仓储：T-13 席位上限依赖它（缺了就静默不校验）。
	memberService, err := app.NewMemberService(store.tenants, store.members, emitter, app.Options{
		Service:    deps.Config.Service,
		Quotas:     store.quotas,
		Observer:   observer,
		RoleSyncer: roleSyncer,
	})
	if err != nil {
		return nil, fmt.Errorf("tenant member service: %w", err)
	}
	quotaService, err := app.NewQuotaService(store.tenants, store.quotas, emitter, app.Options{Service: deps.Config.Service, Observer: observer})
	if err != nil {
		return nil, fmt.Errorf("tenant quota service: %w", err)
	}
	// 邀请用例（T-14）：成员与配额仓储复用同一套实现；TTL 用领域默认值（7 天）。
	invitationService, err := app.NewInvitationService(store.tenants, store.members, store.invitations,
		deps.IDs, emitter, app.Options{Service: deps.Config.Service, Quotas: store.quotas, Observer: observer, RoleSyncer: roleSyncer})
	if err != nil {
		return nil, fmt.Errorf("tenant invitation service: %w", err)
	}

	handler, err := tenantrest.NewHandler(tenantrest.HandlerConfig{
		Service:     service,
		Members:     memberService,
		Quotas:      quotaService,
		Invitations: invitationService,
		PageLimits: pagination.Config{
			DefaultPageSize: defaultPageSize,
			MaxPageSize:     maxPageSize,
		},
		Channel: deps.Config.Channel,
	})
	if err != nil {
		return nil, fmt.Errorf("tenant handler: %w", err)
	}
	if deps.Async != nil {
		if err := RegisterAsync(deps); err != nil {
			return nil, fmt.Errorf("tenant async handlers: %w", err)
		}
	}
	// ENT-15：租户数据处置任务（D-05 第三段）——装配失败不阻断主流程
	// （处置是周期任务，装配异常由日志跟进），但记录错误便于排查。
	if _, perr := buildPurgeService(deps, repo, tenantCfg); perr != nil {
		deps.Logger.Warn("tenant purge service unavailable", "error", perr)
	}
	return &Runtime{Handler: handler, invitationService: invitationService}, nil
}

// RegisterAsync 注册租户域异步 topic handler（API 与 worker 共用）。
//
// 审计事件由 Relay 投递到审计表并旁路结构化日志；表写入失败按可重试错误处理，
// 由 Relay 退避重试（event_id 幂等）。
func RegisterAsync(deps *platform.Deps) error {
	if deps == nil || deps.Async == nil {
		return nil
	}
	emitters := platformaudit.MultiEmitter{}
	if deps.Database != nil {
		emitters = append(emitters, platformaudit.SQLEmitter{DB: deps.Database.SQL()})
	}
	emitters = append(emitters, platformaudit.SlogEmitter{Log: deps.Logger})
	if err := deps.Async.Register(app.TopicAudit, platformaudit.OutboxHandler(emitters)); err != nil {
		return fmt.Errorf("tenant async handler %s: %w", app.TopicAudit, err)
	}
	return nil
}

// NewGuard 构造 B 端租户状态守卫中间件（ENT-09）。
//
// 内存模式（db.driver=memory）返回 nil 表示不启用：内存仓储是进程私有的，
// 与 A 端进程不共享租户表，用它判定会把所有 B 端请求误判为「租户不存在」。
// 生产与集成环境使用 MySQL，守卫必然启用。
//
// 构造期同时写入 deps.VerifyTenant，供 identity 域在登录 / 开通时复用同一判定。
func NewGuard(deps *platform.Deps) (func(http.Handler) http.Handler, error) {
	guard, err := buildGuard(deps)
	if err != nil || guard == nil {
		return nil, err
	}
	deps.VerifyTenant = guard.Verify
	return httpserver.TenantGuardMiddleware(guard.Verify, deps.Config.Channel == "biz"), nil
}

// buildGuard 构造租户状态守卫实例；内存模式返回 nil。
func buildGuard(deps *platform.Deps) (*app.Guard, error) {
	if deps == nil || deps.Config == nil || deps.Database == nil {
		return nil, nil
	}
	store, _, err := buildDeps(deps)
	if err != nil {
		return nil, err
	}
	// T-16：守卫判定 / 缓存命中指标；Metrics 缺失时 Observer 为 nil，
	// app.NewGuard 内部归一化为 noop。
	var observer app.Observer
	if deps.Metrics != nil {
		metrics, mErr := tenantobservability.NewMetrics(deps.Metrics.RegistererWithoutChannel())
		if mErr != nil {
			return nil, fmt.Errorf("tenant guard metrics: %w", mErr)
		}
		observer = metrics
	}
	guard, err := app.NewGuard(store.tenants, app.GuardOptions{Observer: observer})
	if err != nil {
		return nil, fmt.Errorf("tenant guard: %w", err)
	}
	return guard, nil
}

// store 把三组仓储接口打包，便于在同一装配点复用同一份实现。
//
// 内存与 SQL 实现都同时满足三者（methods 命名刻意加 Member/Quota 前缀，
// 避免与租户的 Get/List 签名冲突）。
type store struct {
	tenants     domain.Repository
	members     domain.MemberRepository
	quotas      domain.QuotaRepository
	plans       domain.PlanRepository
	invitations domain.InvitationRepository
	approvals   domain.ApprovalRepository
}

// buildDeps 按数据库可用性选择仓储与审计出口。
//
// 数据库可用时用 MySQL 仓储并把审计写入审计表；内存模式用内存仓储 +
// 结构化日志审计，保证本地开发不需要起库就能走通生命周期。
func buildDeps(deps *platform.Deps) (store, audit.Emitter, error) {
	emitter := audit.Emitter(platformaudit.SlogEmitter{Log: deps.Logger})
	if deps.Database == nil {
		memory := persistence.NewMemoryRepository()
		return store{tenants: memory, members: memory, quotas: memory, plans: memory,
			invitations: memory, approvals: memory}, emitter, nil
	}
	mysqlRepo, err := persistence.NewMySQLRepository(deps.Database.SQL())
	if err != nil {
		return store{}, nil, fmt.Errorf("tenant repository: %w", err)
	}
	// 先构造仓储集合，避免在多返回值里跨行写复合字面量——
	// 该写法在不同 gofmt 版本下缩进判定不一致（CI 与本地会互相打架）。
	st := store{
		tenants:     mysqlRepo,
		members:     mysqlRepo,
		quotas:      mysqlRepo,
		plans:       mysqlRepo,
		invitations: mysqlRepo,
		approvals:   mysqlRepo,
	}
	// 生产优先走 Outbox：审计与业务写入同事务，Relay 负责重试与死信。
	// 异步未启用时退回直写审计表 + 结构化日志（本地 / 单进程模式）。
	if deps.Async != nil && deps.Async.Store() != nil {
		return st, platformaudit.OutboxEmitter{Topic: app.TopicAudit, Events: deps.Async.Store()}, nil
	}
	// 双出口：审计表供查询 / 取证，结构化日志供排障；任一失败不影响另一出口。
	return st, platformaudit.MultiEmitter{
		platformaudit.SQLEmitter{DB: deps.Database.SQL()},
		platformaudit.SlogEmitter{Log: deps.Logger},
	}, nil
}

// membershipChecker 把租户成员仓储适配成 identity 需要的成员关系端口（T-22）。
//
// 语义：在册（active）成员 → true；不存在或已停用 → false；仓储故障 → error
// （调用方必须失败关闭，「查不到」不等于「不是成员」）。
type membershipChecker struct {
	members domain.MemberRepository
}

// IsMember 判定主体是否为租户在册成员（实现 app.MembershipPort）。
func (m membershipChecker) IsMember(ctx context.Context, tenantID, subjectID string) (bool, error) {
	member, err := m.members.GetMember(ctx, tenantID, subjectID)
	if err != nil {
		if errors.Is(err, domain.ErrMemberNotFound) {
			return false, nil
		}
		return false, err
	}
	return member.Active(), nil
}

// InjectMembership 把租户成员仓储适配成通用函数并写入 Deps.IsTenantMember
// （T-22）。必须先于 identityruntime.New 调用——identity 构造期读取该依赖；
// 重复注入无害（同一 Deps 覆盖同值）。memory 与 SQL 模式都提供（本地可用）。
func InjectMembership(deps *platform.Deps) error {
	if deps == nil {
		return fmt.Errorf("tenant membership injection requires deps")
	}
	if deps.IsTenantMember != nil {
		return nil // 已注入（幂等）。
	}
	store, _, err := buildDeps(deps)
	if err != nil {
		return fmt.Errorf("tenant membership injection: %w", err)
	}
	checker := membershipChecker{members: store.members}
	deps.IsTenantMember = checker.IsMember
	return nil
}

// newRoleSyncer 构造成员角色 → IAM 绑定同步器（T-18，ADR-0007）。
//
// IAM 仓储经 iamruntime.BuildRepositories 构造：与租户域同一 *sql.DB
// （或同为内存实现），绑定写入复用 ctx 内的租户事务——这是「成员变更与
// 绑定同步同事务」的前提。deps 未启用 IAM（无 ids 等）时返回 noop。
func newRoleSyncerFromDeps(deps *platform.Deps) (app.RoleSyncer, error) {
	roles, bindings, _, _, err := iamruntime.BuildRepositories(deps)
	if err != nil {
		return nil, fmt.Errorf("tenant role sync: build iam repositories: %w", err)
	}
	// IAM 侧实现租户端口（与 identity 的 TenantStatusChecker / SessionRevoker 同模式）。
	return iamapp.NewRoleSyncer(roles, bindings, deps.IDs)
}
