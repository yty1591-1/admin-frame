package webhook

import (
	"errors"
	"net/http"
)

// Middleware 返回入站验签中间件。
//
// 校验通过后把 Delivery 写入 context（DeliveryFrom），并将 r.Body 复位，
// 下游 Handler 可再次读取原始 body。失败时调用 Config.OnError；未配置时
// 按哨兵写通用文案与状态码：
//
//	413 体积超限 / 409 重放 / 503 重放存储不可用 / 400 读取失败 / 401 其余失败
//
// next 必须非 nil。中间件应放在 Recover、RequestID 等基础中间件之后，
// 并确保上游没有提前读取或替换 r.Body。
func (v *Verifier) Middleware() func(http.Handler) http.Handler {
	if v == nil {
		return func(_ http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "webhook verification misconfigured", http.StatusInternalServerError)
			})
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			delivery, err := v.VerifyRequest(r.Context(), r)
			if err != nil {
				v.writeError(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithDelivery(r.Context(), delivery)))
		})
	}
}

func (v *Verifier) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if v.cfg.OnError != nil {
		v.cfg.OnError(w, r, err)
		return
	}
	status := verificationStatus(err)
	http.Error(w, defaultErrorMessage(status), status)
}

// verificationStatus 将哨兵映射为对外状态码；未知错误按认证失败处理。
func verificationStatus(err error) int {
	switch {
	case errors.Is(err, ErrBodyTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, ErrBodyUnreadable):
		return http.StatusBadRequest
	case errors.Is(err, ErrReplay):
		return http.StatusConflict
	case errors.Is(err, ErrNonceUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, ErrInvalidConfig):
		return http.StatusInternalServerError
	default:
		return http.StatusUnauthorized
	}
}

// defaultErrorMessage 返回不含内部细节的通用文案。
func defaultErrorMessage(status int) string {
	switch status {
	case http.StatusRequestEntityTooLarge:
		return "request body too large"
	case http.StatusBadRequest:
		return "unable to read request body"
	case http.StatusConflict:
		return "duplicate webhook"
	case http.StatusServiceUnavailable:
		return "webhook verification unavailable"
	case http.StatusInternalServerError:
		return "webhook verification misconfigured"
	default:
		return "webhook verification failed"
	}
}
