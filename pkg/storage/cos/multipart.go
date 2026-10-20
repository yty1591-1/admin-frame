package cos

import (
	"context"
	"io"
	"strings"

	tencentcos "github.com/tencentyun/cos-go-sdk-v5"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

// CreateMultipart 实现 storage.MultipartUploader。
func (s *Store) CreateMultipart(ctx context.Context, key string, opts storage.PutOptions) (string, error) {
	if err := s.ready(ctx); err != nil {
		return "", err
	}
	if err := storage.ValidateKey(key); err != nil {
		return "", err
	}
	ct := opts.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	out, _, err := s.client.Object.InitiateMultipartUpload(ctx, key, &tencentcos.InitiateMultipartUploadOptions{
		ObjectPutHeaderOptions: &tencentcos.ObjectPutHeaderOptions{
			ContentType: ct,
			XCosMetaXXX: metaHeader(opts.Metadata),
		},
	})
	if err != nil {
		return "", mapErr(err)
	}
	return out.UploadID, nil
}

// UploadPart 实现 storage.MultipartUploader。
func (s *Store) UploadPart(ctx context.Context, key, uploadID string, partNumber int32, body io.Reader) (string, error) {
	if err := s.ready(ctx); err != nil {
		return "", err
	}
	if err := storage.ValidateKey(key); err != nil {
		return "", err
	}
	if uploadID == "" || partNumber < 1 {
		return "", storage.ErrInvalidConfig
	}
	if body == nil {
		body = strings.NewReader("")
	}
	resp, err := s.client.Object.UploadPart(ctx, key, uploadID, int(partNumber), body, nil)
	if err != nil {
		return "", mapErr(err)
	}
	defer resp.Body.Close()
	return trimETag(resp.Header.Get("ETag")), nil
}

// CompleteMultipart 实现 storage.MultipartUploader。
func (s *Store) CompleteMultipart(ctx context.Context, key, uploadID string, parts []storage.CompletedPart) (storage.ObjectInfo, error) {
	if err := s.ready(ctx); err != nil {
		return storage.ObjectInfo{}, err
	}
	if err := storage.ValidateKey(key); err != nil {
		return storage.ObjectInfo{}, err
	}
	completed := make([]tencentcos.Object, 0, len(parts))
	for _, p := range parts {
		completed = append(completed, tencentcos.Object{
			PartNumber: int(p.PartNumber),
			ETag:       p.ETag,
		})
	}
	_, _, err := s.client.Object.CompleteMultipartUpload(ctx, key, uploadID, &tencentcos.CompleteMultipartUploadOptions{
		Parts: completed,
	})
	if err != nil {
		return storage.ObjectInfo{}, mapErr(err)
	}
	return s.Stat(ctx, key)
}

// AbortMultipart 实现 storage.MultipartUploader。
func (s *Store) AbortMultipart(ctx context.Context, key, uploadID string) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	if err := storage.ValidateKey(key); err != nil {
		return err
	}
	_, err := s.client.Object.AbortMultipartUpload(ctx, key, uploadID)
	return mapErr(err)
}
