package oss

import (
	"context"
	"io"
	"strings"

	alioss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"

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
	out, err := s.client.InitiateMultipartUpload(ctx, &alioss.InitiateMultipartUploadRequest{
		Bucket:      alioss.Ptr(s.bucket),
		Key:         alioss.Ptr(key),
		ContentType: alioss.Ptr(ct),
		Metadata:    opts.Metadata,
	})
	if err != nil {
		return "", mapErr(err)
	}
	return alioss.ToString(out.UploadId), nil
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
	out, err := s.client.UploadPart(ctx, &alioss.UploadPartRequest{
		Bucket:     alioss.Ptr(s.bucket),
		Key:        alioss.Ptr(key),
		UploadId:   alioss.Ptr(uploadID),
		PartNumber: partNumber,
		Body:       body,
	})
	if err != nil {
		return "", mapErr(err)
	}
	return trimETag(alioss.ToString(out.ETag)), nil
}

// CompleteMultipart 实现 storage.MultipartUploader。
func (s *Store) CompleteMultipart(ctx context.Context, key, uploadID string, parts []storage.CompletedPart) (storage.ObjectInfo, error) {
	if err := s.ready(ctx); err != nil {
		return storage.ObjectInfo{}, err
	}
	if err := storage.ValidateKey(key); err != nil {
		return storage.ObjectInfo{}, err
	}
	completed := make([]alioss.UploadPart, 0, len(parts))
	for _, p := range parts {
		completed = append(completed, alioss.UploadPart{
			PartNumber: int32(p.PartNumber),
			ETag:       alioss.Ptr(p.ETag),
		})
	}
	_, err := s.client.CompleteMultipartUpload(ctx, &alioss.CompleteMultipartUploadRequest{
		Bucket:   alioss.Ptr(s.bucket),
		Key:      alioss.Ptr(key),
		UploadId: alioss.Ptr(uploadID),
		CompleteMultipartUpload: &alioss.CompleteMultipartUpload{
			Parts: completed,
		},
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
	_, err := s.client.AbortMultipartUpload(ctx, &alioss.AbortMultipartUploadRequest{
		Bucket:   alioss.Ptr(s.bucket),
		Key:      alioss.Ptr(key),
		UploadId: alioss.Ptr(uploadID),
	})
	return mapErr(err)
}
