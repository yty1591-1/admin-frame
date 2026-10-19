package storage

import (
	"context"
	"io"
)

// CompletedPart 分片上传完成项。
type CompletedPart struct {
	PartNumber int32
	ETag       string
}

// MultipartUploader 大文件分片上传（M5）；Memory/Local 可不实现。
type MultipartUploader interface {
	CreateMultipart(ctx context.Context, key string, opts PutOptions) (uploadID string, err error)
	UploadPart(ctx context.Context, key, uploadID string, partNumber int32, body io.Reader) (etag string, err error)
	CompleteMultipart(ctx context.Context, key, uploadID string, parts []CompletedPart) (ObjectInfo, error)
	AbortMultipart(ctx context.Context, key, uploadID string) error
}

// AsMultipart 若 Store 支持分片则返回（含 PrefixStore 穿透探测）。
func AsMultipart(s Store) (MultipartUploader, bool) {
	if s == nil {
		return nil, false
	}
	if p, ok := s.(*PrefixStore); ok {
		if _, ok := AsMultipart(p.inner); !ok {
			return nil, false
		}
		return p, true
	}
	m, ok := s.(MultipartUploader)
	return m, ok
}
