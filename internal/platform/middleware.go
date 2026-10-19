package platform

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/contextx"
	"github.com/admin-frame/admin-frame/pkg/middleware"
	"github.com/admin-frame/admin-frame/pkg/rbac"
)

// defaultRBAC 演示/本地默认权限表（生产应由 IAM 装配注入）。
// admin 为超级角色；merchant_staff 仅订单读写（不再绕过全部权限点）。
var defaultRBAC = func() *rbac.Checker {
	c := rbac.New(rbac.WithSuperRoles("admin"))
	c.GrantRole("merchant_staff", "order:read", "order:write")
	return c
}()

// TraceIDFromContext 读取请求追踪 ID；缺失时返回空串。
func TraceIDFromContext(ctx context.Context) string {
	return contextx.RequestID(ctx)
}

// Chain 按顺序组合中间件（委托 pkg/middleware）。
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	converted := make([]middleware.Middleware, 0, len(mws))
	for _, mw := range mws {
		if mw != nil {
			converted = append(converted, mw)
		}
	}
	return middleware.Chain(h, converted...)
}

// Recover 捕获 handler panic，记录日志并返回内部错误信封。
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return middleware.Recover(middleware.RecoverConfig{
		Logger: log,
		OnPanic: func(w http.ResponseWriter, r *http.Request, _ any) {
			httpx.FromError(w, apperr.Internal("internal error", nil), TraceIDFromContext(r.Context()))
		},
	})
}

// RequestID 注入/回写 X-Request-ID（委托 pkg/middleware；OBS-05）。
func RequestID() func(http.Handler) http.Handler {
	return middleware.RequestID()
}

// AccessLog 记录访问日志（委托 pkg/middleware；含 trace_id）。
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return middleware.AccessLog(log)
}

// NewAuthMiddleware 按配置返回鉴权中间件。
// demo_mode 为 true 时使用 DemoAuth（仅本地/演示）；为 false 时拒绝启动，
// 避免生产环境静默回退到可伪造的演示令牌（见 docs/SECURITY_STANDARDS.md SEC-04）。
func NewAuthMiddleware(cfg AuthConfig, expected auth.Channel) (func(http.Handler) http.Handler, error) {
	if !cfg.DemoMode {
		return nil, fmt.Errorf("auth.demo_mode=false: production authenticator is not configured")
	}
	if cfg.ExpectedChannel != "" && cfg.ExpectedChannel != string(expected) {
		return nil, fmt.Errorf("auth.expected_channel %q != process channel %q", cfg.ExpectedChannel, expected)
	}
	return DemoAuth(expected), nil
}

// DemoAuth 演示用 Bearer Token 解析，仅允许在 auth.demo_mode=true 时挂载。
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
			ctx = contextx.WithIdentity(ctx, contextx.Identity{
				RequestID:  p.RequestID,
				UserID:     p.SubjectID,
				MerchantID: p.MerchantID,
				Channel:    string(p.Channel),
				Roles:      p.Roles,
			})
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

// RequirePermission 权限点校验（A/B 端；委托 pkg/rbac，默认拒绝）。
func RequirePermission(perm string) func(http.Handler) http.Handler {
	return rbac.Require(rbac.RequireConfig{
		Checker:    defaultRBAC,
		Permission: rbac.Permission(perm),
		SubjectFrom: func(r *http.Request) (rbac.Subject, bool) {
			p, ok := auth.FromContext(r.Context())
			if !ok {
				return rbac.Subject{}, false
			}
			return rbac.Subject{Roles: p.Roles}, true
		},
		OnUnauthenticated: func(w http.ResponseWriter, r *http.Request) {
			httpx.FromError(w, apperr.Unauthorized("unauthorized"), TraceIDFromContext(r.Context()))
		},
		OnDenied: func(w http.ResponseWriter, r *http.Request, need rbac.Permission) {
			httpx.FromError(w, apperr.Forbidden(rbac.Denied(need).Error()), TraceIDFromContext(r.Context()))
		},
	})
}
