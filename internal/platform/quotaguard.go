package platform

import (
	"context"
	"net/http"
	"strconv"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
)

// QuotaGuard 判定某租户的本次请求是否在 API 配额内（ENT-14）。
//
// 与 TenantGuard 同理放在 platform：中间件属于请求链路，具体实现由
// cmd 注入（tenantruntime 包装 tenant/app.QuotaEnforcer），本文件只
// 共享方法签名，避免 platform → tenant 的包依赖。
type QuotaGuard interface {
	// AllowRequest 对租户的本次请求扣减 1 个 API 配额。
	//
	// 返回 retryAfter>0 表示明确拒绝（配额耗尽，统一 42900）；
	// err!=nil 表示执行器故障——按 fail-open 放行（配额执行不应比
	// 业务本身更脆；需要收紧时由后端 failure_mode 承担）。
	AllowRequest(ctx context.Context, tenantID string) (retryAfterSeconds int, err error)
}

// WithQuotaGuard 在租户状态守卫之后执行 API 配额（ENT-14）。
//
// 只对携带生效租户的请求扣减（B 端）；平台级请求（A / C 端）没有
// 租户上下文，不参与租户配额。拒绝响应带 Retry-After（秒）。
//
// 组合顺序：鉴权 → 租户状态 → 配额 → 业务（配额守卫包在 TenantGuard 内层）。
func WithQuotaGuard(mw func(http.Handler) http.Handler, guard QuotaGuard) func(http.Handler) http.Handler {
	if guard == nil {
		return mw
	}
	return func(next http.Handler) http.Handler {
		return mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			retryAfter, err := guard.AllowRequest(r.Context(), tenant)
			if err != nil {
				// 执行器故障 fail-open；不把内部错误暴露给客户端。
				next.ServeHTTP(w, r)
				return
			}
			if retryAfter > 0 {
				traceID := httpx.TraceIDFromContext(r.Context())
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				httpx.FromError(w, apperr.TooManyRequests("api quota exhausted"), traceID)
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}
