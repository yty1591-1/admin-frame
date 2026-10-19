package s3

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/admin-frame/admin-frame/pkg/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// Store 实现 storage.Store 与 storage.Presigner。
type Store struct {
	client   *s3.Client
	presign  *s3.PresignClient
	bucket   string
	maxBytes int64
	putTTL   time.Duration
	getTTL   time.Duration
	closed   atomic.Bool
}

// Put 实现 storage.Store。
func (s *Store) Put(ctx context.Context, key string, body io.Reader, opts storage.PutOptions) (storage.ObjectInfo, error) {
	if err := s.ready(ctx); err != nil {
		return storage.ObjectInfo{}, err
	}
	if err := storage.ValidateKey(key); err != nil {
		return storage.ObjectInfo{}, err
	}
	if body == nil {
		body = strings.NewReader("")
	}
	ct := opts.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	input := &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(ct),
		Metadata:    opts.Metadata,
	}
	out, err := s.client.PutObject(ctx, input)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	info, err := s.Stat(ctx, key)
	if err != nil {
		// Put 成功但 Head 失败时回退最小信息
		return storage.ObjectInfo{
			Key:         key,
			ContentType: ct,
			ETag:        strings.Trim(aws.ToString(out.ETag), `"`),
			Metadata:    cloneMap(opts.Metadata),
		}, nil
	}
	return info, nil
}

// Get 实现 storage.Store。
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, storage.ObjectInfo, error) {
	if err := s.ready(ctx); err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	if err := storage.ValidateKey(key); err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, storage.ObjectInfo{}, mapErr(err)
	}
	info := storage.ObjectInfo{
		Key:          key,
		Size:         aws.ToInt64(out.ContentLength),
		ContentType:  aws.ToString(out.ContentType),
		ETag:         strings.Trim(aws.ToString(out.ETag), `"`),
		Metadata:     cloneMap(out.Metadata),
		LastModified: aws.ToTime(out.LastModified),
	}
	return out.Body, info, nil
}

// Delete 实现 storage.Store。
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	if err := storage.ValidateKey(key); err != nil {
		return err
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return mapErr(err)
}

// Stat 实现 storage.Store。
func (s *Store) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	if err := s.ready(ctx); err != nil {
		return storage.ObjectInfo{}, err
	}
	if err := storage.ValidateKey(key); err != nil {
		return storage.ObjectInfo{}, err
	}
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return storage.ObjectInfo{}, mapErr(err)
	}
	return storage.ObjectInfo{
		Key:          key,
		Size:         aws.ToInt64(out.ContentLength),
		ContentType:  aws.ToString(out.ContentType),
		ETag:         strings.Trim(aws.ToString(out.ETag), `"`),
		Metadata:     cloneMap(out.Metadata),
		LastModified: aws.ToTime(out.LastModified),
	}, nil
}

// Exists 实现 storage.Store。
func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.Stat(ctx, key)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	return false, err
}

// List 实现 storage.Store。
func (s *Store) List(ctx context.Context, opts storage.ListOptions) (storage.ListResult, error) {
	if err := s.ready(ctx); err != nil {
		return storage.ListResult{}, err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 1000
	}
	in := &s3.ListObjectsV2Input{
		Bucket:  aws.String(s.bucket),
		MaxKeys: aws.Int32(int32(limit)),
	}
	if opts.Prefix != "" {
		in.Prefix = aws.String(opts.Prefix)
	}
	if opts.ContinuationToken != "" {
		in.ContinuationToken = aws.String(opts.ContinuationToken)
	}
	out, err := s.client.ListObjectsV2(ctx, in)
	if err != nil {
		return storage.ListResult{}, mapErr(err)
	}
	items := make([]storage.ObjectInfo, 0, len(out.Contents))
	for _, obj := range out.Contents {
		items = append(items, storage.ObjectInfo{
			Key:          aws.ToString(obj.Key),
			Size:         aws.ToInt64(obj.Size),
			ETag:         strings.Trim(aws.ToString(obj.ETag), `"`),
			LastModified: aws.ToTime(obj.LastModified),
		})
	}
	res := storage.ListResult{
		Items:       items,
		IsTruncated: aws.ToBool(out.IsTruncated),
	}
	if res.IsTruncated {
		res.NextToken = aws.ToString(out.NextContinuationToken)
	}
	return res, nil
}

// Close 实现 storage.Store。
func (s *Store) Close() error {
	if s != nil {
		s.closed.Store(true)
	}
	return nil
}

// PresignPut 实现 storage.Presigner。
func (s *Store) PresignPut(ctx context.Context, key string, expiry time.Duration, opts storage.PutOptions) (string, error) {
	if err := s.ready(ctx); err != nil {
		return "", err
	}
	if err := storage.ValidateKey(key); err != nil {
		return "", err
	}
	if expiry <= 0 {
		expiry = s.putTTL
	}
	ct := opts.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	out, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(ct),
		Metadata:    opts.Metadata,
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", err
	}
	return out.URL, nil
}

// PresignGet 实现 storage.Presigner。
func (s *Store) PresignGet(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if err := s.ready(ctx); err != nil {
		return "", err
	}
	if err := storage.ValidateKey(key); err != nil {
		return "", err
	}
	if expiry <= 0 {
		expiry = s.getTTL
	}
	out, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", err
	}
	return out.URL, nil
}

func (s *Store) ready(ctx context.Context) error {
	if s == nil || s.closed.Load() || s.client == nil {
		return storage.ErrClosed
	}
	return ctx.Err()
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return storage.ErrNotFound
	}
	var nsb *types.NotFound
	if errors.As(err, &nsb) {
		return storage.ErrNotFound
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFound", "NoSuchKey", "404":
			return storage.ErrNotFound
		}
	}
	return err
}

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
