package obs

import (
	"context"
	"io"
	"strings"

	hwobs "github.com/huaweicloud/huaweicloud-sdk-go-obs/obs"

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
	in := &hwobs.InitiateMultipartUploadInput{}
	in.Bucket = s.bucket
	in.Key = key
	in.ContentType = ct
	in.Metadata = opts.Metadata
	out, err := s.client.InitiateMultipartUpload(in)
	if err != nil {
		return "", mapErr(err)
	}
	return out.UploadId, nil
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
	in := &hwobs.UploadPartInput{}
	in.Bucket = s.bucket
	in.Key = key
	in.UploadId = uploadID
	in.PartNumber = int(partNumber)
	in.Body = body
	out, err := s.client.UploadPart(in)
	if err != nil {
		return "", mapErr(err)
	}
	return trimETag(out.ETag), nil
}

// CompleteMultipart 实现 storage.MultipartUploader。
func (s *Store) CompleteMultipart(ctx context.Context, key, uploadID string, parts []storage.CompletedPart) (storage.ObjectInfo, error) {
	if err := s.ready(ctx); err != nil {
		return storage.ObjectInfo{}, err
	}
	if err := storage.ValidateKey(key); err != nil {
		return storage.ObjectInfo{}, err
	}
	completed := make([]hwobs.Part, 0, len(parts))
	for _, p := range parts {
		completed = append(completed, hwobs.Part{
			PartNumber: int(p.PartNumber),
			ETag:       p.ETag,
		})
	}
	in := &hwobs.CompleteMultipartUploadInput{}
	in.Bucket = s.bucket
	in.Key = key
	in.UploadId = uploadID
	in.Parts = completed
	_, err := s.client.CompleteMultipartUpload(in)
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
	_, err := s.client.AbortMultipartUpload(&hwobs.AbortMultipartUploadInput{
		Bucket:   s.bucket,
		Key:      key,
		UploadId: uploadID,
	})
	return mapErr(err)
}
