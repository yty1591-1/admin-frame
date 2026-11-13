package idempotency

import (
	"bytes"
	stderrors "errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/pkg/errors"
)

const defaultMaxBody = 1 << 20 // 1 MiB

// DegradedHeader 标记本次写请求在幂等存储不可用时被降级放行（P1-4）。
const DegradedHeader = "X-Idempotency-Degraded"

// Config 配置幂等 HTTP 中间件。
type Config struct {
	// Store 必填。
	Store Store
	// TTL 记录存活时间；≤0 时 24h。
	TTL time.Duration
	// TTLSource 非空时在每次写请求上取当前 TTL，覆盖 TTL（ADR-0015 热更）。
	//
	// 只影响**新写入**的记录；已存在的记录沿用其写入时的过期时间，
	// 避免缩短 TTL 让在途请求的可重放窗口突然消失。
	TTLSource func() time.Duration
	// Header 幂等键头名；空则 Idempotency-Key。
	Header string
	// Required 为 true 时，适用方法缺少键则拒绝（API-11）；默认 false（由调用方显式打开）。
	Required bool
	// Methods 需要幂等的 HTTP 方法；空则 POST/PUT/PATCH。
	Methods []string
	// MaxBodyBytes 参与指纹与缓存的 body 上限；≤0 则 1MiB。
	MaxBodyBytes int64
	// Scope 生成作用域前缀（如 user_id / merchant_id）；nil 则无前缀。
	Scope func(*http.Request) string
	// OnError 解析/冲突/存储错误时的响应；nil 时写纯文本 HTTP 状态。
	OnError func(w http.ResponseWriter, r *http.Request, err error)
	// DegradeOnError 为 true 时，幂等存储不可用仍放行请求，并写入 DegradedHeader（P1-4）。
	// 只对存储不可用生效；缺键 / 键非法 / 指纹冲突仍按原策略拒绝。
	DegradeOnError bool
}

func (c Config) normalize() Config {
	if c.Header == "" {
		c.Header = Header
	}
	if c.TTL <= 0 {
		c.TTL = defaultTTL
	}
	if len(c.Methods) == 0 {
		c.Methods = []string{http.MethodPost, http.MethodPut, http.MethodPatch}
	}
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = defaultMaxBody
	}
	return c
}

// effectiveTTL 返回本次请求使用的记录存活时间。
//
// TTLSource 返回非法值（≤0）时回落到配置值，避免一次坏热更让记录永不过期。
func (c Config) effectiveTTL() time.Duration {
	if c.TTLSource != nil {
		if ttl := c.TTLSource(); ttl > 0 {
			return ttl
		}
	}
	if c.TTL <= 0 {
		return defaultTTL
	}
	return c.TTL
}

// Middleware 对配置的写方法应用幂等键去重与响应回放。
//
// 流程：读键 → 读 body 算指纹 → Begin → 命中 Completed 则回放；
// 新建则捕获下游响应，2xx/4xx Complete，5xx/panic Release。
func Middleware(cfg Config) func(http.Handler) http.Handler {
	cfg = cfg.normalize()
	methodSet := make(map[string]struct{}, len(cfg.Methods))
	for _, m := range cfg.Methods {
		methodSet[strings.ToUpper(m)] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := methodSet[r.Method]; !ok {
				next.ServeHTTP(w, r)
				return
			}
			clientKey := strings.TrimSpace(r.Header.Get(cfg.Header))
			if clientKey == "" {
				if cfg.Required {
					writeErr(cfg, w, r, ErrMissingKey)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if err := ValidateKey(clientKey); err != nil {
				writeErr(cfg, w, r, err)
				return
			}
			if cfg.Store == nil {
				degradeOrFail(cfg, w, r, next, ErrStoreUnavailable)
				return
			}

			body, err := readBody(r, cfg.MaxBodyBytes)
			if err != nil {
				writeErr(cfg, w, r, err)
				return
			}

			scope := ""
			if cfg.Scope != nil {
				scope = cfg.Scope(r)
			}
			storeKey := CompositeKey(scope, clientKey)
			fp := FingerprintRequest(r, body)
			// 同一请求的 Begin 与 Complete 必须用同一个 TTL：
			// 中途热更不应让「创建」和「回写」落在不同过期时间上。
			ttl := cfg.effectiveTTL()

			rec, created, err := cfg.Store.Begin(r.Context(), storeKey, fp, ttl)
			if err != nil {
				degradeOrFail(cfg, w, r, next, err)
				return
			}
			if !created {
				if rec.State == StateCompleted {
					writeStoredResponse(w, rec)
					return
				}
				writeErr(cfg, w, r, ErrInProgress)
				return
			}

			r = r.WithContext(WithKey(r.Context(), clientKey))
			capture := newResponseCapture(w)
			defer func() {
				if recovered := recover(); recovered != nil {
					_ = cfg.Store.Release(r.Context(), storeKey, fp)
					panic(recovered)
				}
			}()

			next.ServeHTTP(capture, r)

			status := capture.status
			if status >= 500 {
				_ = cfg.Store.Release(r.Context(), storeKey, fp)
				capture.flush()
				return
			}
			if cerr := cfg.Store.Complete(r.Context(), storeKey, fp, status, capture.Header(), capture.buf.Bytes(), ttl); cerr != nil {
				_ = cfg.Store.Release(r.Context(), storeKey, fp)
				if cfg.DegradeOnError && stderrors.Is(cerr, ErrStoreUnavailable) {
					// 业务已执行成功，不能因回写失败把 2xx 改写成 5xx；放行原响应并标记。
					capture.Header().Set(DegradedHeader, "true")
					capture.flush()
					return
				}
				writeErr(cfg, w, r, cerr)
				return
			}
			capture.flush()
		})
	}
}

func readBody(r *http.Request, maxBytes int64) ([]byte, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}
	defer func() { _ = r.Body.Close() }()
	limited := io.LimitReader(r.Body, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, errors.InvalidArgument("failed to read request body")
	}
	if int64(len(body)) > maxBytes {
		return nil, ErrBodyTooLarge
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func writeErr(cfg Config, w http.ResponseWriter, r *http.Request, err error) {
	if cfg.OnError != nil {
		cfg.OnError(w, r, err)
		return
	}
	code := errors.HTTPStatusOf(err)
	http.Error(w, err.Error(), code)
}

// degradeOrFail 在存储不可用且开启降级时放行并打标记，否则沿用错误响应。
func degradeOrFail(cfg Config, w http.ResponseWriter, r *http.Request, next http.Handler, err error) {
	if cfg.DegradeOnError && stderrors.Is(err, ErrStoreUnavailable) {
		w.Header().Set(DegradedHeader, "true")
		next.ServeHTTP(w, r)
		return
	}
	writeErr(cfg, w, r, err)
}
