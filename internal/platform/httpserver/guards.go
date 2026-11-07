package httpserver

import (
	"context"
	"net/http"
	"strconv"

	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// TenantGuardMiddleware 在鉴权之后校验租户状态（ENT-09）。
//
// 平台只保留「鉴权链上可插入一个 func(http.Handler) http.Handler」的通用装配点，
// 判定语义由业务域注入的 verify 函数承担（见 internal/tenant/app 的 GuardPort）。
//
// requireMerchant 表示本端所有请求都必须携带商户（B 端）：merchant_id 为空的
// 已认证令牌按 403 拒绝（T-09 fail-closed）。A/C 端传 false。
//
// 组合方式为 auth(guard(next))：鉴权中间件先写入 Principal，守卫再读取它；
// 未认证请求由下游鉴权中间件处理，本层不重复判定。
func TenantGuardMiddleware(verify func(ctx context.Context, merchantID string) (allow bool, err error), requireMerchant bool) func(http.Handler) http.Handler {
	if verify == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					traceID := apiresp.TraceIDFromContext(r.Context())
					apiresp.FromError(w, apperr.Forbidden("merchant context is required"), traceID)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			allow, err := verify(r.Context(), effectiveTenant)
			traceID := apiresp.TraceIDFromContext(r.Context())
			switch {
			case err != nil:
				// 无法判定 → 503 失败关闭；错误只含商户 ID，不含业务数据。
				apiresp.FromError(w, apperr.Unavailable("tenant status unavailable"), traceID)
				return
			case !allow:
				// 统一文案：不区分「未登记」与「已停用」，避免探测租户是否存在。
				apiresp.FromError(w, apperr.Forbidden("tenant is not available"), traceID)
				return
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
}

// QuotaGuardMiddleware 在租户状态守卫之后执行 API 配额（ENT-14）。
//
// 只对携带生效租户的请求扣减（B 端）；平台级请求（A / C 端）没有租户上下文，
// 不参与租户配额。拒绝响应带 Retry-After（秒）。
func QuotaGuardMiddleware(allow func(ctx context.Context, tenantID string) (retryAfterSeconds int, err error)) func(http.Handler) http.Handler {
	if allow == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := auth.FromContext(r.Context())
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			tenant := principal.CurrentTenant()
			if tenant == "" {
				// 平台级请求：无租户上下文，不做租户配额。
				next.ServeHTTP(w, r)
				return
			}
			retryAfter, err := allow(r.Context(), tenant)
			if err != nil {
				// 执行器故障 fail-open；不把内部错误暴露给客户端。
				next.ServeHTTP(w, r)
				return
			}
			if retryAfter > 0 {
				traceID := apiresp.TraceIDFromContext(r.Context())
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				apiresp.FromError(w, apperr.TooManyRequests("api quota exhausted"), traceID)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WithTenantGuard 在鉴权之后校验租户状态（ENT-09）。
//
// 兼容包装：旧签名接收命名接口，现改为接收实现的方法值（tenantapp.Guard.Verify）。
// 业务语义端口已下沉到 internal/tenant/app，平台只保留通用中间件装配点。
func WithTenantGuard(mw func(http.Handler) http.Handler, verify func(ctx context.Context, merchantID string) (bool, error), requireMerchant bool) func(http.Handler) http.Handler {
	return AfterAuth(mw, TenantGuardMiddleware(verify, requireMerchant))
}

// WithQuotaGuard 在租户状态守卫之后执行 API 配额（ENT-14）。
func WithQuotaGuard(mw func(http.Handler) http.Handler, allow func(ctx context.Context, tenantID string) (int, error)) func(http.Handler) http.Handler {
	return AfterAuth(mw, QuotaGuardMiddleware(allow))
}

// AfterAuth 按声明顺序把守卫插入鉴权之后。
//
// guards[0] 最先执行（最外层），最终链路为 auth → guards[0] → guards[1] → handler。
// 显式顺序取代原 Options 中的隐式约定（租户状态在内层、配额更内层）。
func AfterAuth(mw func(http.Handler) http.Handler, guards ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	if len(guards) == 0 {
		return mw
	}
	return func(next http.Handler) http.Handler {
		handler := next
		for i := len(guards) - 1; i >= 0; i-- {
			if guards[i] == nil {
				continue
			}
			handler = guards[i](handler)
		}
		return mw(handler)
	}
}
