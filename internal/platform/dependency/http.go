package dependency

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/admin-frame/admin-frame/pkg/circuitbreaker"
	"github.com/admin-frame/admin-frame/pkg/httpclient"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

// HTTPCaller 是经注册表构造的 HTTP 依赖句柄。
//
// 请求级重试、退避与 Retry-After 语义复用 pkg/httpclient；超时、重试、
// 熔断与指标参数由 Spec 单一来源。
type HTTPCaller struct {
	client *httpclient.Client
}

// Do 发送请求；调用方负责关闭 resp.Body。
func (c *HTTPCaller) Do(req *http.Request) (*http.Response, error) {
	if c == nil || c.client == nil {
		return nil, httpclient.ErrInvalidConfig
	}
	return c.client.Do(req)
}

// HTTP 返回底层 *http.Client（已含注册表的重试 / 熔断 / 观测链）。
func (c *HTTPCaller) HTTP() *http.Client {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.HTTP()
}

// CloseIdleConnections 关闭空闲连接（进程退出前可选调用）。
func (c *HTTPCaller) CloseIdleConnections() {
	if c == nil {
		return
	}
	c.client.CloseIdleConnections()
}

// HTTP 按 Spec 的弹性参数构造 HTTP 依赖句柄。
//
// Spec.Timeout 覆盖 pkg/httpclient 的请求超时；Spec.Retry 映射为请求级
// 重试策略（MaxAttempts<=1 即单次尝试，与 Register 语义一致）；
// Spec.Breaker 以 Transport 装饰器形式作用于每次尝试。
func (r *Registry) HTTP(spec Spec, cfg httpclient.Config) (*HTTPCaller, error) {
	name, err := r.prepare(spec)
	if err != nil {
		return nil, err
	}
	spec.Name = name
	breaker, err := newBreaker(spec)
	if err != nil {
		return nil, err
	}

	cfg.Timeout = orDefault(spec.Timeout, 5*time.Second)
	if spec.Retry.MaxAttempts <= 1 {
		cfg.Retry = retry.Policy{MaxAttempts: 1}
	} else {
		cfg.Retry = spec.Retry
	}
	statuses := cfg.RetryStatusCodes
	if statuses == nil {
		statuses = httpclient.DefaultRetryStatusCodes()
	}
	cfg.RetryStatusCodes = statuses
	cfg.Observer = httpAttemptObserver{name: name, obs: r.observer, statuses: statuses}
	if breaker != nil {
		cfg.Transport = &breakerTransport{
			next:     cfg.TransportOrDefault(),
			breaker:  breaker,
			statuses: statuses,
		}
	}

	client, err := httpclient.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("dependency: build http client for %q: %w", name, err)
	}
	return &HTTPCaller{client: client}, nil
}

// httpAttemptObserver 把 httpclient 的尝试回调映射到注册表指标口径。
type httpAttemptObserver struct {
	name     string
	obs      Observer
	statuses []int
}

// AttemptObserved 记录单次尝试：重试次数、调用结果与熔断打开。
func (o httpAttemptObserver) AttemptObserved(attempt, statusCode int, err error) {
	if attempt > 1 {
		o.obs.RetryObserved(o.name)
	}
	switch {
	case err == nil && !statusFailure(statusCode, o.statuses):
		o.obs.CallObserved(o.name, "ok")
	case isBreakerOpen(err):
		o.obs.CallObserved(o.name, "breaker_open")
	default:
		o.obs.CallObserved(o.name, "error")
	}
}

// breakerTransport 把每次 HTTP 尝试结果计入熔断器。
//
// 传输错误与故障状态码计入失败；熔断打开时快速失败且不触发重试。
// 故障状态码仍返回原响应，调用方语义不变。
type breakerTransport struct {
	next     http.RoundTripper
	breaker  *circuitbreaker.Breaker
	statuses []int
}

var errBreakerStatus = errors.New("dependency: http status failure")

// RoundTrip 实现 http.RoundTripper。
func (t *breakerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var (
		resp     *http.Response
		roundErr error
	)
	err := t.breaker.Do(func() error {
		resp, roundErr = t.next.RoundTrip(req)
		if roundErr != nil {
			return roundErr
		}
		if statusFailure(resp.StatusCode, t.statuses) {
			return errBreakerStatus
		}
		return nil
	})
	if isBreakerOpen(err) {
		return nil, err
	}
	if roundErr != nil {
		return nil, roundErr
	}
	return resp, nil
}

// statusFailure 判定状态码是否计入熔断失败（5xx 与可重试状态码）。
func statusFailure(status int, retryStatuses []int) bool {
	if status >= http.StatusInternalServerError {
		return true
	}
	for _, code := range retryStatuses {
		if status == code {
			return true
		}
	}
	return false
}
