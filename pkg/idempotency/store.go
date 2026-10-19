package idempotency

import (
	"context"
	"net/http"
	"time"
)

// State 幂等记录状态。
type State int

const (
	// StateInProgress 已占用、业务尚未完成。
	StateInProgress State = iota + 1
	// StateCompleted 已完成并缓存了可回放响应。
	StateCompleted
)

// Record 是存储中的幂等记录。
type Record struct {
	Key         string
	Fingerprint string
	State       State
	StatusCode  int
	Header      http.Header
	Body        []byte
	ExpiresAt   time.Time
}

// Clone 深拷贝 Header/Body，避免调用方篡改存储。
func (r Record) Clone() Record {
	out := r
	if r.Header != nil {
		out.Header = r.Header.Clone()
	}
	if r.Body != nil {
		out.Body = append([]byte(nil), r.Body...)
	}
	return out
}

// Store 幂等去重存储（内存 / Redis / DB 均可实现）。
//
// Begin 必须原子：同键并发仅一个 created=true。
// Complete 仅允许 InProgress 且指纹匹配的记录转入 Completed。
// Release 仅释放 InProgress（用于 5xx / panic 后允许重试）。
type Store interface {
	Begin(ctx context.Context, key, fingerprint string, ttl time.Duration) (rec Record, created bool, err error)
	Complete(ctx context.Context, key, fingerprint string, statusCode int, header http.Header, body []byte, ttl time.Duration) error
	Release(ctx context.Context, key, fingerprint string) error
}
