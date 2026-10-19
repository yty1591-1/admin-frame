package storage

import (
	"context"
	"io"
	"strings"
)

// PrefixStore 为所有键强制加上固定前缀（租户隔离常用）。
type PrefixStore struct {
	inner  Store
	prefix string // 不含尾 /
}

// WithPrefix 包装 Store；prefix 经 ValidateKey（可用 Join 生成）。
func WithPrefix(inner Store, prefix string) (*PrefixStore, error) {
	if inner == nil {
		return nil, ErrInvalidConfig
	}
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return nil, ErrInvalidKey
	}
	if err := ValidateKey(prefix); err != nil {
		return nil, err
	}
	return &PrefixStore{inner: inner, prefix: prefix}, nil
}

func (p *PrefixStore) fullKey(key string) (string, error) {
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	// 前缀已在 WithPrefix 校验；完整键 = prefix + "/" + key（key 可含多段）
	full := p.prefix + "/" + key
	if err := ValidateKey(full); err != nil {
		return "", err
	}
	return full, nil
}

func (p *PrefixStore) strip(key string) string {
	pre := p.prefix + "/"
	return strings.TrimPrefix(key, pre)
}

// Put 实现 Store。
func (p *PrefixStore) Put(ctx context.Context, key string, body io.Reader, opts PutOptions) (ObjectInfo, error) {
	fk, err := p.fullKey(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	info, err := p.inner.Put(ctx, fk, body, opts)
	if err != nil {
		return ObjectInfo{}, err
	}
	info.Key = key
	return info, nil
}

// Get 实现 Store。
func (p *PrefixStore) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	fk, err := p.fullKey(key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	rc, info, err := p.inner.Get(ctx, fk)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	info.Key = key
	return rc, info, nil
}

// Delete 实现 Store。
func (p *PrefixStore) Delete(ctx context.Context, key string) error {
	fk, err := p.fullKey(key)
	if err != nil {
		return err
	}
	return p.inner.Delete(ctx, fk)
}

// Stat 实现 Store。
func (p *PrefixStore) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	fk, err := p.fullKey(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	info, err := p.inner.Stat(ctx, fk)
	if err != nil {
		return ObjectInfo{}, err
	}
	info.Key = key
	return info, nil
}

// Exists 实现 Store。
func (p *PrefixStore) Exists(ctx context.Context, key string) (bool, error) {
	fk, err := p.fullKey(key)
	if err != nil {
		return false, err
	}
	return p.inner.Exists(ctx, fk)
}

// List 实现 Store；相对前缀列举。
func (p *PrefixStore) List(ctx context.Context, opts ListOptions) (ListResult, error) {
	innerOpts := opts
	if opts.Prefix == "" {
		innerOpts.Prefix = p.prefix + "/"
	} else {
		fk, err := Join(p.prefix, opts.Prefix)
		if err != nil {
			// 允许 prefix 为相对路径片段
			innerOpts.Prefix = p.prefix + "/" + strings.Trim(opts.Prefix, "/")
		} else {
			innerOpts.Prefix = fk
		}
	}
	if opts.ContinuationToken != "" {
		fk, err := p.fullKey(opts.ContinuationToken)
		if err != nil {
			return ListResult{}, err
		}
		innerOpts.ContinuationToken = fk
	}
	res, err := p.inner.List(ctx, innerOpts)
	if err != nil {
		return ListResult{}, err
	}
	for i := range res.Items {
		res.Items[i].Key = p.strip(res.Items[i].Key)
	}
	if res.NextToken != "" {
		res.NextToken = p.strip(res.NextToken)
	}
	return res, nil
}

// Close 实现 Store。
func (p *PrefixStore) Close() error {
	if p == nil || p.inner == nil {
		return nil
	}
	return p.inner.Close()
}
