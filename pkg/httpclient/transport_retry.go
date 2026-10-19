package httpclient

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

type retryTransport struct {
	next          http.RoundTripper
	policy        retry.Policy
	nonIdempotent bool
	retryStatuses []int
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil || t.next == nil {
		return nil, ErrInvalidConfig
	}
	if req == nil {
		return nil, ErrNilRequest
	}
	if !t.allowsRetry(req.Method) {
		return t.next.RoundTrip(req)
	}
	if err := ensureRewindable(req); err != nil {
		return nil, err
	}

	policy := t.policy
	policy.RetryIf = t.isRetryable
	var lastResp *http.Response

	err := retry.Do(req.Context(), policy, func(ctx context.Context) error {
		attempt, err := cloneRequest(req, ctx)
		if err != nil {
			return retry.Permanent(err)
		}
		if delay := retryAfterDelay(lastResp); delay > 0 {
			if err := sleepCtx(ctx, delay); err != nil {
				return err
			}
		}
		resp, err := t.next.RoundTrip(attempt)
		if err != nil {
			closeResponse(lastResp)
			lastResp = nil
			return mapNetError(err)
		}
		if statusRetryable(resp.StatusCode, t.retryStatuses) {
			closeResponse(lastResp)
			lastResp = resp
			return &StatusError{
				StatusCode: resp.StatusCode,
				Method:     req.Method,
				URL:        redactURL(req),
			}
		}
		closeResponse(lastResp)
		lastResp = resp
		return nil
	})

	if err == nil {
		return lastResp, nil
	}
	if lastResp != nil && isStatusOrExhausted(err) {
		return lastResp, nil
	}
	closeResponse(lastResp)
	return nil, err
}

func (t *retryTransport) allowsRetry(method string) bool {
	return methodIdempotent(method) || t.nonIdempotent
}

func (t *retryTransport) isRetryable(err error) bool {
	if err == nil || retry.IsPermanent(err) {
		return false
	}
	var se *StatusError
	if stderrors.As(err, &se) {
		return statusRetryable(se.StatusCode, t.retryStatuses)
	}
	return pkgerrors.RetryableOf(err)
}

func isStatusOrExhausted(err error) bool {
	var se *StatusError
	if stderrors.As(err, &se) {
		return true
	}
	return retry.IsExhausted(err)
}

func ensureRewindable(req *http.Request) error {
	if req.Body == nil || req.Body == http.NoBody {
		return nil
	}
	if req.GetBody == nil {
		return retry.Permanent(fmt.Errorf("%w: request body is not rewindable (set GetBody)", ErrInvalidConfig))
	}
	return nil
}

func cloneRequest(req *http.Request, ctx context.Context) (*http.Request, error) {
	out := req.Clone(ctx)
	if req.Body == nil || req.Body == http.NoBody {
		return out, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	out.Body = body
	return out, nil
}

func mapNetError(err error) error {
	if err == nil {
		return nil
	}
	if stderrors.Is(err, context.Canceled) || stderrors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var ne net.Error
	if stderrors.As(err, &ne) {
		return pkgerrors.Unavailable(err.Error()).WithRetryable(true)
	}
	// 常见临时错误字符串兜底
	return pkgerrors.Unavailable(err.Error()).WithRetryable(true)
}

func closeResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

func retryAfterDelay(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	ra := resp.Header.Get("Retry-After")
	if ra == "" {
		return 0
	}
	if sec, err := strconv.Atoi(ra); err == nil && sec > 0 {
		return time.Duration(sec) * time.Second
	}
	if at, err := http.ParseTime(ra); err == nil {
		d := time.Until(at)
		if d > 0 {
			return d
		}
	}
	return 0
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
