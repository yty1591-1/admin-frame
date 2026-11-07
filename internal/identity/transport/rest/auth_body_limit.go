package rest

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// AuthBodyLimitBytes 是认证 / 账号处置路由的独立请求体上限（SEC-03）。
//
// 认证请求体只包含主体、密码、令牌、MFA 码等短字段；16KiB 足够覆盖所有合法请求，
// 同时把未认证请求可触发的内存分配从全局 32MiB 收敛到 16KiB。
const AuthBodyLimitBytes int64 = 16 << 10

// AuthBodyLimit 返回认证路由的请求体上限中间件。
//
// Content-Length 已超限时在进入 Handler 前直接返回 40000；未知长度（chunked）由
// MaxBytesReader 在读取时阻断，并由严格解码统一映射为 40000。
func AuthBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > AuthBodyLimitBytes {
			apiresp.FromError(w, apperr.BadRequest("request body too large"),
				apiresp.TraceIDFromContext(r.Context()))
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, AuthBodyLimitBytes)
		}
		next.ServeHTTP(w, r)
	})
}
