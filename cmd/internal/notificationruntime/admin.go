package notificationruntime

import (
	"fmt"

	"github.com/admin-frame/admin-frame/internal/notification/app"
	"github.com/admin-frame/admin-frame/internal/notification/observability"
	"github.com/admin-frame/admin-frame/internal/platform"
)

// AdminServices 是 A 端管理服务集（NT-06）：查询 / 模板 / 补偿 / 代发。
//
// 与完整 Runtime（编排）的差别：不建渠道路由与黑名单盐——A 端只写
// Outbox 事件，编排消费在 worker 进程（ADR-0008）。
type AdminServices struct {
	// Queries 通知查询用例。
	Queries *app.QueryService
	// Templates 模板用例。
	Templates *app.TemplateService
	// Replay DLQ 补偿用例。
	Replay *app.ReplayService
	// Send 代发用例。
	Send *app.SendService
	// Dispatcher 生产侧出口（邀请通知适配等复用）。
	Dispatcher *app.OutboxDispatcher
}

// NewAdminServices 构造 A 端管理服务；async 未启用返回 nil（调用方跳过注册，
// 单进程最小形态保持「只落库不发送」阶段语义）。
func NewAdminServices(deps *platform.Deps) (*AdminServices, error) {
	if deps == nil || deps.Config == nil || deps.IDs == nil {
		return nil, nil
	}
	if deps.Async == nil || deps.Async.Store() == nil {
		return nil, nil
	}
	repos, err := buildRepositories(deps)
	if err != nil {
		return nil, err
	}
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

	queries, err := app.NewQueryService(repos.requests, repos.deliveries)
	if err != nil {
		return nil, fmt.Errorf("notification queries: %w", err)
	}
	templates, err := app.NewTemplateService(repos.templates, deps.IDs, emitter, opts)
	if err != nil {
		return nil, fmt.Errorf("notification templates: %w", err)
	}
	dispatcher, err := app.NewOutboxDispatcher(deps.Async.Store())
	if err != nil {
		return nil, fmt.Errorf("notification dispatcher: %w", err)
	}
	replay, err := app.NewReplayService(repos.requests, dispatcher, repos.dlq, deps.IDs, emitter, opts)
	if err != nil {
		return nil, fmt.Errorf("notification replay: %w", err)
	}
	send, err := app.NewSendService(dispatcher, deps.IDs, emitter, opts)
	if err != nil {
		return nil, fmt.Errorf("notification send: %w", err)
	}
	return &AdminServices{
		Queries:    queries,
		Templates:  templates,
		Replay:     replay,
		Send:       send,
		Dispatcher: dispatcher,
	}, nil
}
