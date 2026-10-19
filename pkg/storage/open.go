package storage

import (
	"context"
	"fmt"
	"strings"
)

func init() {
	Register("memory", openMemory)
	Register("local", openLocal)
}

// Open 按 Config.Driver 构造 Store；未知驱动返回 ErrUnknownDriver。
//
// 云驱动须 blank-import 对应子包，例如：
//
//	import _ "github.com/admin-frame/admin-frame/pkg/storage/s3"
func Open(ctx context.Context, cfg Config) (Store, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	driver := normalizeDriver(cfg.Driver)
	if driver == "" {
		return nil, fmt.Errorf("%w: driver is required", ErrInvalidConfig)
	}
	open, err := lookup(driver)
	if err != nil {
		return nil, err
	}
	store, err := open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	prefix := strings.Trim(cfg.KeyPrefix, "/")
	if prefix == "" {
		return store, nil
	}
	return WithPrefix(store, prefix)
}

func openMemory(_ context.Context, cfg Config) (Store, error) {
	return NewMemory(MemoryConfig{MaxObjectBytes: cfg.MaxObjectBytes}), nil
}

func openLocal(_ context.Context, cfg Config) (Store, error) {
	lc := cfg.Local
	if lc.MaxObjectBytes == 0 {
		lc.MaxObjectBytes = cfg.MaxObjectBytes
	}
	return OpenLocal(lc)
}
