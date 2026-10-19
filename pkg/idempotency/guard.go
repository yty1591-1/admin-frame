package idempotency

import (
	"context"
	"net/http"
	"time"
)

const defaultTTL = 24 * time.Hour

// Result 是 Guard 缓存的业务结果（非 HTTP 场景也可复用 StatusCode/Body）。
type Result struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// Guard 在应用层按键+指纹执行一次并缓存结果。
type Guard struct {
	Store Store
	TTL   time.Duration
}

// Run 执行 fn：首次创建并 Complete；命中已完成则回放；冲突/进行中返回错误。
// fn 返回 error 且未产出可缓存结果时 Release，允许重试。
func (g *Guard) Run(ctx context.Context, key, fingerprint string, fn func(context.Context) (Result, error)) (Result, error) {
	if g == nil || g.Store == nil {
		return Result{}, ErrStoreUnavailable
	}
	if err := ValidateKey(key); err != nil {
		return Result{}, err
	}
	if fingerprint == "" {
		return Result{}, ErrInvalidKey
	}
	ttl := g.TTL
	if ttl <= 0 {
		ttl = defaultTTL
	}

	rec, created, err := g.Store.Begin(ctx, key, fingerprint, ttl)
	if err != nil {
		return Result{}, err
	}
	if !created {
		if rec.State == StateCompleted {
			return Result{
				StatusCode: rec.StatusCode,
				Header:     rec.Header.Clone(),
				Body:       append([]byte(nil), rec.Body...),
			}, nil
		}
		return Result{}, ErrInProgress
	}

	res, err := fn(ctx)
	if err != nil {
		_ = g.Store.Release(ctx, key, fingerprint)
		return Result{}, err
	}
	if res.StatusCode >= 500 {
		_ = g.Store.Release(ctx, key, fingerprint)
		return res, nil
	}
	if res.StatusCode == 0 {
		res.StatusCode = http.StatusOK
	}
	if cerr := g.Store.Complete(ctx, key, fingerprint, res.StatusCode, res.Header, res.Body, ttl); cerr != nil {
		_ = g.Store.Release(ctx, key, fingerprint)
		return Result{}, cerr
	}
	return res, nil
}
