package obs

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	hwobs "github.com/huaweicloud/huaweicloud-sdk-go-obs/obs"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

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
	in := &hwobs.PutObjectInput{}
	in.Bucket = s.bucket
	in.Key = key
	in.Body = body
	in.ContentType = ct
	in.Metadata = opts.Metadata
	_, err := s.client.PutObject(in)
	if err != nil {
		return storage.ObjectInfo{}, mapErr(err)
	}
	info, err := s.Stat(ctx, key)
	if err != nil {
		return storage.ObjectInfo{
			Key:         key,
			ContentType: ct,
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
	out, err := s.client.GetObject(&hwobs.GetObjectInput{
		GetObjectMetadataInput: hwobs.GetObjectMetadataInput{
			Bucket: s.bucket,
			Key:    key,
		},
	})
	if err != nil {
		return nil, storage.ObjectInfo{}, mapErr(err)
	}
	info := storage.ObjectInfo{
		Key:          key,
		Size:         out.ContentLength,
		ContentType:  out.ContentType,
		ETag:         trimETag(out.ETag),
		Metadata:     cloneMap(out.Metadata),
		LastModified: out.LastModified,
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
	_, err := s.client.DeleteObject(&hwobs.DeleteObjectInput{
		Bucket: s.bucket,
		Key:    key,
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
	out, err := s.client.GetObjectMetadata(&hwobs.GetObjectMetadataInput{
		Bucket: s.bucket,
		Key:    key,
	})
	if err != nil {
		return storage.ObjectInfo{}, mapErr(err)
	}
	return storage.ObjectInfo{
		Key:          key,
		Size:         out.ContentLength,
		ContentType:  out.ContentType,
		ETag:         trimETag(out.ETag),
		Metadata:     cloneMap(out.Metadata),
		LastModified: out.LastModified,
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
	in := &hwobs.ListObjectsInput{}
	in.Bucket = s.bucket
	in.MaxKeys = limit
	in.Prefix = opts.Prefix
	in.Marker = opts.ContinuationToken
	out, err := s.client.ListObjects(in)
	if err != nil {
		return storage.ListResult{}, mapErr(err)
	}
	items := make([]storage.ObjectInfo, 0, len(out.Contents))
	for _, obj := range out.Contents {
		items = append(items, storage.ObjectInfo{
			Key:          obj.Key,
			Size:         obj.Size,
			ETag:         trimETag(obj.ETag),
			LastModified: obj.LastModified,
		})
	}
	res := storage.ListResult{
		Items:       items,
		IsTruncated: out.IsTruncated,
	}
	if res.IsTruncated {
		res.NextToken = out.NextMarker
	}
	return res, nil
}

// Close 实现 storage.Store。
func (s *Store) Close() error {
	if s != nil {
		s.closed.Store(true)
		if s.client != nil {
			s.client.Close()
		}
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
	in := &hwobs.CreateSignedUrlInput{}
	in.Method = hwobs.HttpMethodPut
	in.Bucket = s.bucket
	in.Key = key
	in.Expires = int(expiry.Seconds())
	if ct := opts.ContentType; ct != "" {
		in.Headers = map[string]string{"Content-Type": ct}
	}
	out, err := s.client.CreateSignedUrl(in)
	if err != nil {
		return "", mapErr(err)
	}
	return out.SignedUrl, nil
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
	in := &hwobs.CreateSignedUrlInput{}
	in.Method = hwobs.HttpMethodGet
	in.Bucket = s.bucket
	in.Key = key
	in.Expires = int(expiry.Seconds())
	out, err := s.client.CreateSignedUrl(in)
	if err != nil {
		return "", mapErr(err)
	}
	return out.SignedUrl, nil
}
