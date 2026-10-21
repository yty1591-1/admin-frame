package security

import (
	"context"
	"fmt"

	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/authz"
)

// SnapshotLoader 加载主体有效权限快照。
//
// 这是消费侧定义的端口（S-07）：internal/security 不 import internal/iam，
// 具体实现由 cmd 在装配期注入（internal/iam/app.AuthorizeService）。
//
// 返回错误表示鉴权依赖不可用；enforce 模式下调用方必须失败关闭（503 / 50300），
// 不得降级为放行，也不得回退到令牌中的 roles / permissions。
type SnapshotLoader interface {
	LoadSnapshot(ctx context.Context, p auth.Principal) (authz.Snapshot, error)
}

// Mode 是鉴权数据来源模式（docs/rbac-iam.md §15.1）。
type Mode string

const (
	// ModeOff 使用装配期静态角色表（rollback 位），不加载 IAM 快照。
	ModeOff Mode = "off"
	// ModeShadow 计算 IAM 快照但按静态表放行，仅记录差异，不影响请求。
	ModeShadow Mode = "shadow"
	// ModeEnforce 以 IAM 快照为唯一真相；加载失败即 503，默认拒绝。
	ModeEnforce Mode = "enforce"
)

// ParseMode 解析配置中的模式字符串；空串视为 ModeOff。
func ParseMode(raw string) (Mode, error) {
	switch Mode(raw) {
	case "", ModeOff:
		return ModeOff, nil
	case ModeShadow:
		return ModeShadow, nil
	case ModeEnforce:
		return ModeEnforce, nil
	default:
		return "", fmt.Errorf("security: unsupported iam mode %q (want off|shadow|enforce)", raw)
	}
}

// Mismatch 是 shadow 模式下静态表与 IAM 快照的判定差异。
//
// 差异必须为 0 或逐条解释后才能切到 enforce；指标标签只允许布尔值，
// 避免高基数。
type Mismatch struct {
	// Channel 端别（admin / biz）。
	Channel string
	// Permission 被判定的权限点。
	Permission string
	// StaticAllow 静态角色表的判定结果。
	StaticAllow bool
	// IAMAllow IAM 快照的判定结果。
	IAMAllow bool
}

// ShadowObserver 观测 shadow 差异与快照加载错误；实现通常是指标 + 告警。
//
// 观测方法不得阻塞请求，也不得 panic；实现应为并发安全。
type ShadowObserver interface {
	// OnMismatch 记录一次判定差异。
	OnMismatch(ctx context.Context, m Mismatch)
	// OnLoadError 记录 shadow 模式下快照加载失败（此时仍按静态表放行）。
	OnLoadError(ctx context.Context, channel string, err error)
}

// NopShadowObserver 是不做任何观测的默认实现。
type NopShadowObserver struct{}

// OnMismatch 实现 ShadowObserver。
func (NopShadowObserver) OnMismatch(context.Context, Mismatch) {}

// OnLoadError 实现 ShadowObserver。
func (NopShadowObserver) OnLoadError(context.Context, string, error) {}

// 鉴权决策结果标签（对应 iam_authz_decisions_total 的 result）。
const (
	// DecisionAllow 放行。
	DecisionAllow = "allow"
	// DecisionDeny 默认拒绝。
	DecisionDeny = "deny"
	// DecisionUnavailable 鉴权依赖不可用（失败关闭）。
	DecisionUnavailable = "unavailable"
)

// DecisionObserver 观测动态鉴权决策；实现须并发安全且不得阻塞请求。
//
// 仅 mode=shadow|enforce 产生决策事件；off 模式保持静态表历史行为。
type DecisionObserver interface {
	// OnDecision 记录一次决策结果（allow / deny / unavailable）。
	OnDecision(ctx context.Context, channel, result string)
}

// NopDecisionObserver 是不做任何观测的默认实现。
type NopDecisionObserver struct{}

// OnDecision 实现 DecisionObserver。
func (NopDecisionObserver) OnDecision(context.Context, string, string) {}
