package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// 默认排除的探针 / 运维路径，避免污染 RED 与基数。
var defaultExcludePaths = map[string]struct{}{
	"/healthz": {},
	"/readyz":  {},
	"/metrics": {},
	"/version": {},
}

// DefaultDurationBuckets 是 HTTP 延迟直方图默认桶（秒）。
var DefaultDurationBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// HTTP 聚合 HTTP 服务端 RED 指标（Rate / Errors 由 status 派生，Duration 为直方图）。
type HTTP struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight prometheus.Gauge
}

func newHTTP(reg prometheus.Registerer, namespace string) (*HTTP, error) {
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "http_server_requests_total",
		Help:      "Total number of HTTP server requests.",
	}, []string{"method", "code", "route"})

	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "http_server_request_duration_seconds",
		Help:      "HTTP server request duration in seconds.",
		Buckets:   DefaultDurationBuckets,
	}, []string{"method", "code", "route"})

	inFlight := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "http_server_requests_in_flight",
		Help:      "Number of HTTP server requests currently being processed.",
	})

	for _, c := range []prometheus.Collector{requests, duration, inFlight} {
		if err := reg.Register(c); err != nil {
			return nil, err
		}
	}
	return &HTTP{
		requests: requests,
		duration: duration,
		inFlight: inFlight,
	}, nil
}

// HTTPOption 配置 HTTP 中间件行为。
type HTTPOption func(*httpMiddleware)

type httpMiddleware struct {
	exclude    map[string]struct{}
	routeLabel func(*http.Request) string
}

// WithExcludePaths 追加不计入 RED 的路径（精确匹配 URL.Path）。
func WithExcludePaths(paths ...string) HTTPOption {
	return func(m *httpMiddleware) {
		for _, p := range paths {
			m.exclude[p] = struct{}{}
		}
	}
}

// WithRouteLabel 自定义 route 标签解析；默认优先 Request.Pattern，否则 "unknown"。
// 返回值应是低基数模板（如 /admin/v1/orders/{id}），禁止回传原始 query。
func WithRouteLabel(fn func(*http.Request) string) HTTPOption {
	return func(m *httpMiddleware) {
		if fn != nil {
			m.routeLabel = fn
		}
	}
}

// Middleware 返回记录 RED 指标的中间件。
//
// 标签约定：
//   - method：大写 HTTP 方法
//   - code：状态码字符串
//   - route：Pattern 或自定义标签；未知为 "unknown"
func (h *HTTP) Middleware(opts ...HTTPOption) func(http.Handler) http.Handler {
	cfg := &httpMiddleware{
		exclude:    copyExclude(defaultExcludePaths),
		routeLabel: defaultRouteLabel,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, skip := cfg.exclude[r.URL.Path]; skip {
				next.ServeHTTP(w, r)
				return
			}

			h.inFlight.Inc()
			defer h.inFlight.Dec()

			start := time.Now()
			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)

			route := cfg.routeLabel(r)
			if route == "" {
				route = "unknown"
			}
			method := r.Method
			code := strconv.Itoa(rw.status)
			h.requests.WithLabelValues(method, code, route).Inc()
			h.duration.WithLabelValues(method, code, route).Observe(time.Since(start).Seconds())
		})
	}
}

func defaultRouteLabel(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	// Go 1.22+ ServeMux 填充 Pattern（含方法前缀时取其路径部分）。
	if p := r.Pattern; p != "" {
		return normalizePattern(p)
	}
	return "unknown"
}

// normalizePattern 去掉 "GET /path" 中的方法前缀，保留路径模板。
func normalizePattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "unknown"
	}
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		rest := strings.TrimSpace(pattern[i+1:])
		if rest != "" {
			return rest
		}
	}
	return pattern
}

func copyExclude(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader 记录状态码并转发给底层 ResponseWriter。
func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap 支持 ResponseController / 可选接口透传。
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
