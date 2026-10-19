package s3

import (
	"context"
	"io"
	"strings"

	"github.com/admin-frame/admin-frame/pkg/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
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
	out, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(ct),
		Metadata:    opts.Metadata,
	})
	if err != nil {
		return "", err
	}
	return aws.ToString(out.UploadId), nil
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
	out, err := s.client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(s.bucket),
		Key:        aws.String(key),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(partNumber),
		Body:       body,
	})
	if err != nil {
		return "", err
	}
	return strings.Trim(aws.ToString(out.ETag), `"`), nil
}

// CompleteMultipart 实现 storage.MultipartUploader。
func (s *Store) CompleteMultipart(ctx context.Context, key, uploadID string, parts []storage.CompletedPart) (storage.ObjectInfo, error) {
	if err := s.ready(ctx); err != nil {
		return storage.ObjectInfo{}, err
	}
	if err := storage.ValidateKey(key); err != nil {
		return storage.ObjectInfo{}, err
	}
	completed := make([]types.CompletedPart, 0, len(parts))
	for _, p := range parts {
		completed = append(completed, types.CompletedPart{
			ETag:       aws.String(p.ETag),
			PartNumber: aws.Int32(p.PartNumber),
		})
	}
	_, err := s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completed,
		},
	})
	if err != nil {
		return storage.ObjectInfo{}, err
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
	_, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	return err
}
