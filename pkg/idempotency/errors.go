package idempotency

import "github.com/admin-frame/admin-frame/pkg/errors"

var (
	// ErrMissingKey 缺少 Idempotency-Key。
	ErrMissingKey = errors.InvalidArgument("Idempotency-Key required")
	// ErrInvalidKey 键格式非法。
	ErrInvalidKey = errors.InvalidArgument("invalid Idempotency-Key")
	// ErrConflict 同键但请求指纹不一致（REL-04 → 40900）。
	ErrConflict = errors.AlreadyExists("idempotency key conflict: payload mismatch")
	// ErrInProgress 同键请求仍在处理中。
	ErrInProgress = errors.Aborted("idempotency key request in progress")
	// ErrBodyTooLarge 请求体超过指纹/缓存上限。
	ErrBodyTooLarge = errors.InvalidArgument("request body too large for idempotency")
	// ErrStoreUnavailable 存储不可用。
	ErrStoreUnavailable = errors.Unavailable("idempotency store unavailable")
)
