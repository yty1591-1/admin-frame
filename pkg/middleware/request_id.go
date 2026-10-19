package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/admin-frame/admin-frame/pkg/contextx"
	"github.com/admin-frame/admin-frame/pkg/tracing"
)

const headerRequestID = "X-Request-ID"

// RequestIDConfig 配置请求关联 ID 中间件。
type RequestIDConfig struct {
	// Header 入站/出站头名；空则 X-Request-ID。
	Header string
	// Generator 在无入站 ID 且无 OTel TraceID 时生成；nil 用默认随机。
	Generator func() string
}

// RequestID 注入/回写关联 ID，并写入 contextx（OBS-05）。
//
// 须挂在 tracing.HTTPMiddleware 之后：无入站头时优先复用 OTel TraceID。
func RequestID(cfg ...RequestIDConfig) Middleware {
	var c RequestIDConfig
	if len(cfg) > 0 {
		c = cfg[0]
	}
	if c.Header == "" {
		c.Header = headerRequestID
	}
	if c.Generator == nil {
		c.Generator = newRequestID
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(c.Header)
			if id == "" {
				if tid := tracing.TraceID(r.Context()); tid != "" {
					id = tid
				} else {
					id = c.Generator()
				}
			}
			w.Header().Set(c.Header, id)
			ctx := contextx.WithRequestID(r.Context(), id)
			tracing.SetAttributes(ctx, tracing.Attr("request.id", id))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
