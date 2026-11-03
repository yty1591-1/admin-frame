package platform

import (
	"context"
	"net/http"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
)

// TenantGuard 判定某个商户当前是否可服务（ENT-09 停用生效链路）。
//
// 放在 platform 而不是租户域：中间件属于请求链路，platform 不反向依赖业务域；
// 具体实现由 cmd 注入（tenantruntime），与本文件只共享这个方法签名。
type TenantGuard interface {
	// Verify 返回是否放行。
	//
	// allow=false 且 err=nil 表示「明确拒绝」（租户停用 / 终止 / 未登记）；
	// err!=nil 表示「无法判定」（仓储故障），调用方必须按 503 失败关闭，
	// 不得降级为放行 —— 否则一次数据库抖动就等于全平台停用失效。
	Verify(ctx context.Context, merchantID string) (allow bool, err error)
}

// TenantMembership 判定主体是否为租户在册成员（ENT-09 T-22）。
//
// 由租户域实现并注入 Deps；identity 域的切换端点经此校验成员关系，
// 与 TenantGuard 一样通过接口反转避免 platform → tenant 的包依赖。
type TenantMembership interface {
	IsMember(ctx context.Context, tenantID, subjectID string) (bool, error)
}

// WithTenantGuard 在鉴权之后、业务处理之前校验租户状态。
//
// requireMerchant 表示本端所有请求都必须携带商户（B 端）：merchant_id 为空的
// 已认证令牌按 403 拒绝（T-09 fail-closed）——签发缺陷不应静默绕过租户校验。
// A/C 端传 false：平台级请求没有 merchant_id，无需守卫。
//
// 组合方式为 `mw(guard(handler))`：鉴权中间件先写入 Principal 并调用下一个 handler，
// 守卫因此能读到 Principal；未认证请求由下游鉴权中间件处理，本层不重复判定。
func WithTenantGuard(mw func(http.Handler) http.Handler, guard TenantGuard, requireMerchant bool) func(http.Handler) http.Handler {
	if guard == nil {
		return mw
	}
	return func(next http.Handler) http.Handler {
		return mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := auth.FromContext(r.Context())
			if !ok {
				// 鉴权中间件没有写入 Principal（例如公开路径）：交给下游决定。
				next.ServeHTTP(w, r)
				return
			}
			// 生效租户取 current_tenant 声明（T-22），旧令牌回落 merchant_id。
			// 平台级请求（A 端、C 端单租户）没有租户上下文，无需守卫；
			// B 端缺租户属于令牌缺陷，按 403 失败关闭而不是放行。
			effectiveTenant := principal.CurrentTenant()
			if effectiveTenant == "" {
				if requireMerchant {
					traceID := httpx.TraceIDFromContext(r.Context())
					httpx.FromError(w, apperr.Forbidden("merchant context is required"), traceID)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			allow, err := guard.Verify(r.Context(), effectiveTenant)
			traceID := httpx.TraceIDFromContext(r.Context())
			switch {
			case err != nil:
				// 无法判定 → 503 失败关闭；错误只含商户 ID，不含业务数据。
				httpx.FromError(w, apperr.Unavailable("tenant status unavailable"), traceID)
				return
			case !allow:
				// 统一文案：不区分「未登记」与「已停用」，避免探测租户是否存在。
				httpx.FromError(w, apperr.Forbidden("tenant is not available"), traceID)
				return
			default:
				next.ServeHTTP(w, r)
			}
		}))
	}
}
