package httpclient

import (
	"net/http"
)

// Client 包装 http.Client：强制超时、可选重试与出站追踪。
type Client struct {
	http      *http.Client
	userAgent string
}

// New 根据 Config 创建客户端。
func New(cfg Config) (*Client, error) {
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	rt := cfg.buildTransport()
	if !cfg.DisableRetry {
		rt = &retryTransport{
			next:          rt,
			policy:        cfg.Retry,
			nonIdempotent: cfg.RetryNonIdempotent,
			retryStatuses: cfg.RetryStatusCodes,
			observer:      cfg.Observer,
		}
	}
	if !cfg.DisableTracing {
		inject := true
		if cfg.InjectRequestID != nil {
			inject = *cfg.InjectRequestID
		}
		rt = &traceTransport{
			next:            rt,
			tracer:          cfg.Tracer,
			propagator:      cfg.Propagator,
			injectRequestID: inject,
		}
	}

	return &Client{
		http: &http.Client{
			Transport: rt,
			Timeout:   cfg.Timeout,
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
		userAgent: cfg.UserAgent,
	}, nil
}

// HTTP 返回底层 *http.Client（已含 Transport 链）；勿改为无超时。
func (c *Client) HTTP() *http.Client {
	if c == nil {
		return nil
	}
	return c.http
}

// Do 发送请求；自动补全 User-Agent。调用方负责关闭 resp.Body。
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if c == nil || c.http == nil {
		return nil, ErrInvalidConfig
	}
	if req == nil {
		return nil, ErrNilRequest
	}
	if req.Header.Get("User-Agent") == "" && c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	return c.http.Do(req) //nolint:gosec // 通用 HTTP 客户端，目标地址由调用方显式指定
}

// CloseIdleConnections 关闭空闲连接（进程退出前可选调用）。
func (c *Client) CloseIdleConnections() {
	if c == nil || c.http == nil {
		return
	}
	c.http.CloseIdleConnections()
}
