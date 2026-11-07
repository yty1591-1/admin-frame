package httpclient

import (
	"fmt"
	"net/http"
)

// 默认可重试状态码（幂等方法）。
var defaultRetryStatuses = []int{
	http.StatusRequestTimeout,     // 408
	http.StatusTooManyRequests,    // 429
	http.StatusBadGateway,         // 502
	http.StatusServiceUnavailable, // 503
	http.StatusGatewayTimeout,     // 504
}

// DefaultRetryStatusCodes 返回默认可重试状态码副本。
func DefaultRetryStatusCodes() []int {
	return append([]int(nil), defaultRetryStatuses...)
}

// StatusError 表示耗尽重试后仍得到可重试类状态，或调用方主动检查非 2xx。
type StatusError struct {
	StatusCode int
	Method     string
	URL        string
}

// Error 实现 error。
func (e *StatusError) Error() string {
	if e == nil {
		return "httpclient: status error"
	}
	return fmt.Sprintf("httpclient: %s %s: HTTP %d", e.Method, e.URL, e.StatusCode)
}

// CheckStatus 若 status 不在 [200,300) 则返回 *StatusError。
func CheckStatus(resp *http.Response) error {
	if resp == nil {
		return ErrNilRequest
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	u := ""
	method := ""
	if resp.Request != nil {
		method = resp.Request.Method
		if resp.Request.URL != nil {
			u = resp.Request.URL.Redacted()
		}
	}
	return &StatusError{StatusCode: resp.StatusCode, Method: method, URL: u}
}

func statusRetryable(code int, codes []int) bool {
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}

func methodIdempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}
