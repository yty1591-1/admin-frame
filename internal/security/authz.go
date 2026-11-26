package security

import (
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/authz"
	"github.com/admin-frame/admin-frame/pkg/rbac"
)

// errSnapshotMismatch 表示快照主体与 principal 不一致；enforce 下必须失败关闭。
var errSnapshotMismatch = stderrors.New("security: snapshot subject mismatch")

// Authorizer 把 rbac.Checker 适配为 HTTP 权限中间件工厂。
//
// 两种运行方式：
//   - 静态（mode=off 或未注入 loader）：仅用装配期角色表判定，行为与历史版本一致；
//   - 动态（mode=shadow|enforce）：先经 SnapshotLoader 加载快照，再用快照中的角色 /
//     权限构造 rbac.Subject。shadow 仅记录差异并仍按静态表放行；enforce 以快照为
//     唯一真相，加载失败一律失败关闭（503 / 50300）。
//
// 本包不 import internal/iam；具体 loader 由 cmd 注入（S-07 / docs/rbac-iam.md §6.2）。
type Authorizer struct {
	// checker 是装配期静态角色表，同时作为 shadow 模式的放行依据。
	checker *rbac.Checker
	// dynamic 是动态模式的判定器，不注册任何静态角色；仅使用快照权限。
	dynamic *rbac.Checker
	loader  SnapshotLoader
	mode    Mode
	// modeSet 标记调用方显式设置过模式；未显式设置时注入 loader 即 enforce。
	modeSet  bool
	observer ShadowObserver
	// decisions 记录动态鉴权结果，供指标 / 告警。
	decisions DecisionObserver
}

// Option 配置 Authorizer。
type Option func(*Authorizer)

// WithSnapshotLoader 注入动态快照加载器；未显式指定模式时默认 ModeEnforce。
//
// loader 为 nil 时等价于不注入，继续使用静态表。
func WithSnapshotLoader(loader SnapshotLoader) Option {
	return func(a *Authorizer) {
		a.loader = loader
	}
}

// WithMode 显式设置鉴权模式；非法值按 ModeOff 处理并保持静态行为（fail-safe）。
func WithMode(mode Mode) Option {
	return func(a *Authorizer) {
		switch mode {
		case ModeShadow, ModeEnforce:
			a.mode = mode
		default:
			a.mode = ModeOff
		}
		a.modeSet = true
	}
}

// WithShadowObserver 注入 shadow 差异 / 加载失败观测器。
func WithShadowObserver(observer ShadowObserver) Option {
	return func(a *Authorizer) {
		if observer != nil {
			a.observer = observer
		}
	}
}

// WithDecisionObserver 注入动态鉴权决策观测器（allow / deny / unavailable）。
func WithDecisionObserver(observer DecisionObserver) Option {
	return func(a *Authorizer) {
		if observer != nil {
			a.decisions = observer
		}
	}
}

// NewAuthorizer 包装 checker；nil 时生成默认拒绝（fail-closed）的实例。
//
// 默认 mode=off：只有注入 SnapshotLoader 或显式 WithMode 才启用动态鉴权。
func NewAuthorizer(checker *rbac.Checker, opts ...Option) *Authorizer {
	a := &Authorizer{
		checker:   checker,
		dynamic:   rbac.New(),
		mode:      ModeOff,
		observer:  NopShadowObserver{},
		decisions: NopDecisionObserver{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	if a.loader != nil && !a.modeSet {
		a.mode = ModeEnforce
	}
	if a.loader == nil {
		a.mode = ModeOff
	}
	return a
}

// Require 返回校验单个权限点的中间件；未认证 401，无权限 403。
//
// 返回类型与 internal/shared/auth.PermissionMiddleware 一致，便于 cmd 直接注入
// transport，而不让 transport 依赖平台层。
func (a *Authorizer) Require(permission string) func(http.Handler) http.Handler {
	return a.guard([]rbac.Permission{rbac.Permission(permission)}, false)
}

// RequireAny 返回「拥有任一权限点即放行」的中间件。
func (a *Authorizer) RequireAny(permissions ...string) func(http.Handler) http.Handler {
	needs := make([]rbac.Permission, 0, len(permissions))
	for _, p := range permissions {
		needs = append(needs, rbac.Permission(p))
	}
	return a.guard(needs, true)
}

// guard 构造统一的权限中间件；静态与动态分支共用同一套错误响应。
func (a *Authorizer) guard(needs []rbac.Permission, anyMatch bool) func(http.Handler) http.Handler {
	if a == nil || a.mode == ModeOff || a.loader == nil {
		inner := a.staticGuard(needs, anyMatch)
		// 机器主体不走角色表：静态模式下也必须只按 scope 判定（§21.2.3）。
		return func(next http.Handler) http.Handler {
			chain := inner(next)
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if p, ok := auth.FromContext(r.Context()); ok && p.IsClient() {
					if !scopesAllow(p.Scopes, needs, anyMatch) {
						writeDenied(w, r)
						return
					}
					next.ServeHTTP(w, r)
					return
				}
				chain.ServeHTTP(w, r)
			})
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			principal, ok := auth.FromContext(ctx)
			if !ok {
				writeUnauthenticated(w, r)
				return
			}
			// 机器主体只按 scope 判定：不做 IAM 快照、不看角色（§21.2.3）。
			if principal.IsClient() {
				if !scopesAllow(principal.Scopes, needs, anyMatch) {
					a.decisions.OnDecision(ctx, string(principal.Channel), DecisionDeny)
					writeDenied(w, r)
					return
				}
				a.decisions.OnDecision(ctx, string(principal.Channel), DecisionAllow)
				next.ServeHTTP(w, r)
				return
			}
			// shadowFallback 记录失败后按静态表放行 / 拒绝，确保 shadow 不改变请求行为。
			shadowFallback := func(reason error) {
				a.observer.OnLoadError(ctx, string(principal.Channel), reason)
				if !allows(a.checker, rbac.Subject{Roles: principal.Roles}, needs, anyMatch) {
					writeDenied(w, r)
					return
				}
				next.ServeHTTP(w, r)
			}

			snapshot, cached := authz.FromContext(ctx)
			if !cached {
				loaded, err := a.loader.LoadSnapshot(ctx, principal)
				if err != nil {
					if a.mode == ModeShadow {
						shadowFallback(err)
						return
					}
					writeLoadError(w, r, err)
					a.decisions.OnDecision(ctx, string(principal.Channel), DecisionUnavailable)
					return
				}
				if !snapshotMatches(loaded, principal) {
					if a.mode == ModeShadow {
						shadowFallback(errSnapshotMismatch)
						return
					}
					writeLoadError(w, r, apperr.Unavailable(errSnapshotMismatch.Error()))
					a.decisions.OnDecision(ctx, string(principal.Channel), DecisionUnavailable)
					return
				}
				snapshot = loaded
			}
			if !snapshotMatches(snapshot, principal) {
				if a.mode == ModeShadow {
					shadowFallback(errSnapshotMismatch)
					return
				}
				writeLoadError(w, r, apperr.Unavailable(errSnapshotMismatch.Error()))
				a.decisions.OnDecision(ctx, string(principal.Channel), DecisionUnavailable)
				return
			}

			iamAllow := allows(a.dynamic, subjectFromSnapshot(snapshot), needs, anyMatch)

			if a.mode == ModeShadow {
				staticAllow := allows(a.checker, rbac.Subject{Roles: principal.Roles}, needs, anyMatch)
				if staticAllow != iamAllow {
					a.observer.OnMismatch(ctx, Mismatch{
						Channel:     string(principal.Channel),
						Permission:  permissionLabel(needs),
						StaticAllow: staticAllow,
						IAMAllow:    iamAllow,
					})
				}
				// shadow 期同样记录 IAM 侧判定（allow/deny）：切 enforce 前运维需要看到
				// IAM 的判定分布，只有「差异数」不足以判断切换风险（ENT-06 T6 实测：
				// 不记录时 iam_authz_decisions_total 恒为空，看板面板全空）。
				a.decisions.OnDecision(ctx, string(principal.Channel), decisionResult(iamAllow))
				if !staticAllow {
					writeDenied(w, r)
					return
				}
				// shadow 不得改变请求行为：不把快照写入 context，避免 app 据此
				// 收缩数据范围而与静态表放行结果不一致。
				next.ServeHTTP(w, r)
				return
			}

			if !iamAllow {
				a.decisions.OnDecision(ctx, string(principal.Channel), DecisionDeny)
				writeDenied(w, r)
				return
			}
			a.decisions.OnDecision(ctx, string(principal.Channel), DecisionAllow)
			next.ServeHTTP(w, r.WithContext(authz.WithSnapshot(ctx, snapshot)))
		})
	}
}

// staticGuard 是 mode=off 的历史路径：仅依赖装配期角色表。
func (a *Authorizer) staticGuard(needs []rbac.Permission, anyMatch bool) func(http.Handler) http.Handler {
	var checker *rbac.Checker
	if a != nil {
		checker = a.checker
	}
	return rbac.Require(rbac.RequireConfig{
		Checker:    checker,
		Any:        anyNeeds(needs, anyMatch),
		Permission: rbac.Permission(permissionLabel(needs)),
		SubjectFrom: func(r *http.Request) (rbac.Subject, bool) {
			p, ok := auth.FromContext(r.Context())
			if !ok {
				return rbac.Subject{}, false
			}
			return rbac.Subject{Roles: p.Roles}, true
		},
		OnUnauthenticated: func(w http.ResponseWriter, r *http.Request) {
			writeUnauthenticated(w, r)
		},
		OnDenied: func(w http.ResponseWriter, r *http.Request, _ rbac.Permission) {
			writeDenied(w, r)
		},
	})
}

// anyNeeds 仅在 RequireAny 时返回集合；单个权限点走 RequireConfig.Permission。
func anyNeeds(needs []rbac.Permission, anyMatch bool) []rbac.Permission {
	if !anyMatch {
		return nil
	}
	return needs
}

// subjectFromSnapshot 把快照权限转换为 rbac.Subject；角色仅用于诊断。
func subjectFromSnapshot(snapshot authz.Snapshot) rbac.Subject {
	permissions := make([]rbac.Permission, 0, len(snapshot.Permissions))
	for _, code := range snapshot.Permissions {
		permissions = append(permissions, rbac.Permission(code))
	}
	return rbac.Subject{Roles: snapshot.Roles, Permissions: permissions}
}

// scopesAllow 判断机器主体 scope 是否满足权限需求。
//
// 只做精确字符串匹配：不接受通配符，也不做前缀 / 大小写放宽，
// 避免「申请一次宽 scope 永久越权」。scope 为空即拒绝（403）。
func scopesAllow(scopes []string, needs []rbac.Permission, anyMatch bool) bool {
	if len(scopes) == 0 || len(needs) == 0 {
		return false
	}
	granted := make(map[string]struct{}, len(scopes))
	for _, item := range scopes {
		granted[strings.TrimSpace(item)] = struct{}{}
	}
	if anyMatch {
		for _, need := range needs {
			if _, ok := granted[need.String()]; ok {
				return true
			}
		}
		return false
	}
	for _, need := range needs {
		if _, ok := granted[need.String()]; !ok {
			return false
		}
	}
	return true
}

// allows 按 need 集合判定；与 rbac.Checker 的 Allow / AllowAny 语义一致。
func allows(checker *rbac.Checker, subject rbac.Subject, needs []rbac.Permission, anyMatch bool) bool {
	if checker == nil || len(needs) == 0 {
		return false
	}
	if anyMatch {
		return checker.AllowAny(subject, needs...)
	}
	return checker.Allow(subject, needs[0])
}

// decisionResult 把判定布尔值映射为决策标签（allow / deny）。
func decisionResult(allow bool) string {
	if allow {
		return DecisionAllow
	}
	return DecisionDeny
}

// permissionLabel 返回用于错误信息 / 指标的权限点标签。
func permissionLabel(needs []rbac.Permission) string {
	if len(needs) == 0 {
		return ""
	}
	return needs[0].String()
}

// snapshotMatches 校验快照身份与 principal 一致，防止缓存串租户 / 串主体。
func snapshotMatches(snapshot authz.Snapshot, p auth.Principal) bool {
	return snapshot.Channel == string(p.Channel) &&
		snapshot.SubjectID == p.SubjectID &&
		snapshot.MerchantID == p.MerchantID
}

// writeUnauthenticated 写 401 信封。
func writeUnauthenticated(w http.ResponseWriter, r *http.Request) {
	apiresp.FromError(w, apperr.Unauthorized("unauthorized"), apiresp.TraceIDFromContext(r.Context()))
}

// writeDenied 写 403 信封。
func writeDenied(w http.ResponseWriter, r *http.Request) {
	apiresp.FromError(w, apperr.Forbidden("permission denied"), apiresp.TraceIDFromContext(r.Context()))
}

// writeLoadError 把 loader 错误映射为响应：
//   - 鉴权依赖不可用 → 503 / 50300（失败关闭，禁止放行）；
//   - 已带 4xx 业务码的身份错误（如主体非法）→ 原样返回，避免把 401 变成 503。
func writeLoadError(w http.ResponseWriter, r *http.Request, err error) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	var appErr *apperr.AppError
	if stderrors.As(err, &appErr) && appErr.Code >= 40000 && appErr.Code < 50000 {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.FromError(w, apperr.Unavailable("authorization service unavailable"), traceID)
}
