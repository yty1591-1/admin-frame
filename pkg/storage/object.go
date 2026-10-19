package storage

import (
	"context"
	"io"
	"time"
)

// ObjectInfo 对象元数据（不含正文）。
type ObjectInfo struct {
	Key          string
	Size         int64
	ContentType  string
	ETag         string
	Metadata     map[string]string
	LastModified time.Time
}

// PutOptions 写入选项。
type PutOptions struct {
	ContentType string
	Metadata    map[string]string
	// MaxBytes 覆盖 Store 默认大小上限；0 表示用 Store 配置；<0 表示不限制（慎用）。
	MaxBytes int64
}

// ListOptions 列举选项。
type ListOptions struct {
	Prefix string
	// Limit 最大返回条数；≤0 表示默认 1000。
	Limit int
	// ContinuationToken 上一页 NextToken。
	ContinuationToken string
}

// ListResult 列举结果。
type ListResult struct {
	Items         []ObjectInfo
	NextToken     string
	IsTruncated   bool
}

// Store 对象存储核心接口。
type Store interface {
	Put(ctx context.Context, key string, body io.Reader, opts PutOptions) (ObjectInfo, error)
	Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error)
	Delete(ctx context.Context, key string) error
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	Exists(ctx context.Context, key string) (bool, error)
	List(ctx context.Context, opts ListOptions) (ListResult, error)
	Close() error
}

// Presigner 预签名 URL（浏览器直传/下载）；云驱动实现。
type Presigner interface {
	PresignPut(ctx context.Context, key string, expiry time.Duration, opts PutOptions) (string, error)
	PresignGet(ctx context.Context, key string, expiry time.Duration) (string, error)
}

// AsPresigner 若 Store 同时实现 Presigner 则返回。
func AsPresigner(s Store) (Presigner, bool) {
	p, ok := s.(Presigner)
	return p, ok
}
