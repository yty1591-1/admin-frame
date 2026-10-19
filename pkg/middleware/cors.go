package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CORSConfig 跨域配置；生产须显式 AllowOrigins，禁止反射任意 Origin。
type CORSConfig struct {
	// AllowOrigins 允许的 Origin 列表；含 "*" 时不可与 AllowCredentials 同开。
	AllowOrigins []string
	// AllowMethods 默认 GET,HEAD,POST,PUT,PATCH,DELETE,OPTIONS。
	AllowMethods []string
	// AllowHeaders 默认 Accept,Authorization,Content-Type,X-Request-ID。
	AllowHeaders []string
	// ExposeHeaders 暴露给浏览器的响应头。
	ExposeHeaders []string
	// AllowCredentials 是否允许 Cookie；与 AllowOrigins="*" 互斥。
	AllowCredentials bool
	// MaxAge 预检缓存；≤0 不写 Access-Control-Max-Age。
	MaxAge time.Duration
}

func (c *CORSConfig) applyDefaults() {
	if len(c.AllowMethods) == 0 {
		c.AllowMethods = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions}
	}
	if len(c.AllowHeaders) == 0 {
		c.AllowHeaders = []string{"Accept", "Authorization", "Content-Type", "X-Request-ID"}
	}
}

// CORS 处理预检与简单跨域头。
func CORS(cfg CORSConfig) Middleware {
	cfg.applyDefaults()
	methods := strings.Join(cfg.AllowMethods, ", ")
	headers := strings.Join(cfg.AllowHeaders, ", ")
	expose := strings.Join(cfg.ExposeHeaders, ", ")
	var maxAge string
	if cfg.MaxAge > 0 {
		maxAge = strconv.Itoa(int(cfg.MaxAge.Seconds()))
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if allow, ok := matchOrigin(origin, cfg.AllowOrigins, cfg.AllowCredentials); ok {
					w.Header().Set("Access-Control-Allow-Origin", allow)
					w.Header().Add("Vary", "Origin")
					if cfg.AllowCredentials {
						w.Header().Set("Access-Control-Allow-Credentials", "true")
					}
					if expose != "" {
						w.Header().Set("Access-Control-Expose-Headers", expose)
					}
				}
			}

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.Header().Set("Access-Control-Allow-Methods", methods)
				w.Header().Set("Access-Control-Allow-Headers", headers)
				if maxAge != "" {
					w.Header().Set("Access-Control-Max-Age", maxAge)
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func matchOrigin(origin string, allowed []string, credentials bool) (string, bool) {
	if len(allowed) == 0 {
		return "", false
	}
	for _, a := range allowed {
		if a == "*" {
			if credentials {
				return "", false // 规范禁止 * + credentials
			}
			return "*", true
		}
		if strings.EqualFold(a, origin) {
			return origin, true
		}
	}
	return "", false
}
