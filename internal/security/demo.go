package security

import (
	"net/http"
	"strings"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/errors"
)

// NewDemoAuth 返回演示用 Bearer Token 解析中间件。
//
// 仅允许在 auth.mode=demo（local/dev/test）时挂载；生产必须使用 NewJWTAuth，
// 否则可伪造令牌直接冒充任意主体（SECURITY_STANDARDS SEC-04）。
//
//	admin: Bearer admin.<subject_id>
//	biz:   Bearer biz.<merchant_id>.<subject_id>
//	c:     Bearer c.<user_id>
func NewDemoAuth(expected auth.Channel) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			traceID := httpx.TraceIDFromContext(r.Context())
			token, ok := bearerToken(r)
			if !ok {
				httpx.FromError(w, apperr.Unauthorized("missing bearer token"), traceID)
				return
			}
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
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
		})
	}
}

// bearerToken 提取 Authorization: Bearer 令牌；缺失或格式非法时返回 false。
func bearerToken(r *http.Request) (string, bool) {
	raw := r.Header.Get("Authorization")
	if !strings.HasPrefix(raw, "Bearer ") {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
	return token, token != ""
}

// parseDemoToken 解析演示令牌；格式或端别非法时返回未认证错误。
func parseDemoToken(token string) (auth.Principal, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return auth.Principal{}, errors.ErrUnauthenticated
	}
	switch auth.Channel(parts[0]) {
	case auth.ChannelAdmin:
		if len(parts) != 2 || parts[1] == "" {
			return auth.Principal{}, errors.ErrUnauthenticated
		}
		return auth.Principal{
			Channel:   auth.ChannelAdmin,
			SubjectID: parts[1],
			Roles:     []string{"admin"},
		}, nil
	case auth.ChannelBiz:
		if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
			return auth.Principal{}, errors.ErrUnauthenticated
		}
		return auth.Principal{
			Channel:    auth.ChannelBiz,
			MerchantID: parts[1],
			SubjectID:  parts[2],
			Roles:      []string{"merchant_staff"},
		}, nil
	case auth.ChannelC:
		if len(parts) != 2 || parts[1] == "" {
			return auth.Principal{}, errors.ErrUnauthenticated
		}
		return auth.Principal{
			Channel:   auth.ChannelC,
			SubjectID: parts[1],
		}, nil
	default:
		return auth.Principal{}, errors.ErrUnauthenticated
	}
}
