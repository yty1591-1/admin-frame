package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
)

type ctxTraceKey struct{}

// TraceIDFromContext 读取请求追踪 ID；缺失时返回空串。
func TraceIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxTraceKey{}).(string); ok {
		return v
	}
	return ""
}

func withTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxTraceKey{}, id)
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // 演示 ID；失败时退化为零值亦可接受
	return hex.EncodeToString(b)
}

// Chain 按顺序组合中间件。
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// Recover 捕获 handler panic，记录日志并返回内部错误响应。
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered", "panic", rec, "path", r.URL.Path)
					httpx.FromError(w, apperr.Internal("internal error", nil), TraceIDFromContext(r.Context()))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RequestID 注入/回写 X-Request-ID，并写入 context 供日志关联。
func RequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if id == "" {
				id = newID()
			}
			w.Header().Set("X-Request-ID", id)
			ctx := withTraceID(r.Context(), id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AccessLog 记录访问日志（方法、路径、状态、耗时、trace_id）。
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			log.Info("access",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"trace_id", TraceIDFromContext(r.Context()),
			)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader 记录状态码并转发给底层 ResponseWriter（实现 http.ResponseWriter）。
func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// DemoAuth 演示用 Bearer Token 解析，生产替换为各端独立 JWT 校验。
//
//	admin: Bearer admin.<subject_id>
//	biz:   Bearer biz.<merchant_id>.<subject_id>
//	c:     Bearer c.<user_id>
func DemoAuth(expected auth.Channel) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			traceID := TraceIDFromContext(r.Context())
			raw := r.Header.Get("Authorization")
			if !strings.HasPrefix(raw, "Bearer ") {
				httpx.FromError(w, apperr.Unauthorized("missing bearer token"), traceID)
				return
			}
			token := strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
			p, err := parseDemoToken(token)
			if err != nil {
				httpx.FromError(w, apperr.Unauthorized("invalid token"), traceID)
				return
			}
			if p.Channel != expected {
				httpx.FromError(w, apperr.Unauthorized("channel mismatch"), traceID)
				return
			}
			p.RequestID = traceID
			ctx := auth.WithPrincipal(r.Context(), p)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func parseDemoToken(token string) (auth.Principal, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return auth.Principal{}, apperr.ErrUnauthorized
	}
	switch auth.Channel(parts[0]) {
	case auth.ChannelAdmin:
		if len(parts) != 2 || parts[1] == "" {
			return auth.Principal{}, apperr.ErrUnauthorized
		}
		return auth.Principal{
			Channel:   auth.ChannelAdmin,
			SubjectID: parts[1],
			Roles:     []string{"admin"},
		}, nil
	case auth.ChannelBiz:
		if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
			return auth.Principal{}, apperr.ErrUnauthorized
		}
		return auth.Principal{
			Channel:    auth.ChannelBiz,
			MerchantID: parts[1],
			SubjectID:  parts[2],
			Roles:      []string{"merchant_staff"},
		}, nil
	case auth.ChannelC:
		if len(parts) != 2 || parts[1] == "" {
			return auth.Principal{}, apperr.ErrUnauthorized
		}
		return auth.Principal{
			Channel:   auth.ChannelC,
			SubjectID: parts[1],
		}, nil
	default:
		return auth.Principal{}, apperr.ErrUnauthorized
	}
}

// RequirePermission 简单权限点校验（A/B 端）。
func RequirePermission(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := auth.FromContext(r.Context())
			if !ok {
				httpx.FromError(w, apperr.Unauthorized("unauthorized"), TraceIDFromContext(r.Context()))
				return
			}
			for _, role := range p.Roles {
				if role == "admin" || role == perm || role == "merchant_staff" {
					next.ServeHTTP(w, r)
					return
				}
			}
			httpx.FromError(w, apperr.Forbidden("permission denied: "+perm), TraceIDFromContext(r.Context()))
		})
	}
}
