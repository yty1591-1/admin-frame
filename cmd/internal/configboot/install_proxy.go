package configboot

import (
	"io"
	"net/http"
	"strings"
)

// InstallProxy 把 Admin 安装 API 转发到 config-api（携带 mTLS 客户端）。
//
// 浏览器无法向 prod-like config-api 出示 PEM 客户端证书；安装向导经
// admin-api 同源代理，避免「HTTP 打到 HTTPS」的 400。
type InstallProxy struct {
	baseURL string
	client  *http.Client
}

// NewInstallProxy 构造代理；baseURL 为 CONFIG_API_URL，client 须已装配 mTLS。
func NewInstallProxy(baseURL string, client *http.Client) *InstallProxy {
	if client == nil {
		client = http.DefaultClient
	}
	return &InstallProxy{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		client:  client,
	}
}

// Register 挂载无 JWT 的安装代理路由（setup token 由 config-api 校验）。
func (p *InstallProxy) Register(mux *http.ServeMux) {
	if p == nil || mux == nil || p.baseURL == "" {
		return
	}
	mux.HandleFunc("GET /admin/v1/install/status", p.forward("GET", "/internal/v1/install/status"))
	mux.HandleFunc("POST /admin/v1/install/begin", p.forward("POST", "/internal/v1/install/begin"))
	mux.HandleFunc("POST /admin/v1/install/apply", p.forward("POST", "/internal/v1/install/apply"))
	mux.HandleFunc("POST /admin/v1/install/complete", p.forward("POST", "/internal/v1/install/complete"))
}

func (p *InstallProxy) forward(method, upstreamPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body io.Reader
		if r.Body != nil && method != http.MethodGet {
			body = r.Body
		}
		req, err := http.NewRequestWithContext(r.Context(), method, p.baseURL+upstreamPath, body)
		if err != nil {
			http.Error(w, "install proxy build request failed", http.StatusBadGateway)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "" {
			req.Header.Set("Content-Type", ct)
		} else if method != http.MethodGet {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")
		if tok := r.Header.Get(InstallSetupTokenHeader); tok != "" {
			req.Header.Set(InstallSetupTokenHeader, tok)
		}
		// 透传追踪头，便于联调。
		if tid := r.Header.Get("X-Trace-Id"); tid != "" {
			req.Header.Set("X-Trace-Id", tid)
		}
		resp, err := p.client.Do(req)
		if err != nil {
			http.Error(w, "install proxy upstream unavailable: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		for _, h := range []string{"Content-Type", "X-Trace-Id"} {
			if v := resp.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}
