package configclient

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DirCache 按 namespace 落盘配置快照（ADR-0016 本地灾备）。
//
// 目录权限 0700，文件权限 0600；不签名，依赖主机与容器权限边界。
type DirCache struct {
	Dir string
}

// NewDirCache 构造目录缓存；dir 必填。
func NewDirCache(dir string) (*DirCache, error) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return nil, fmt.Errorf("configclient: cache dir is required")
	}
	if err := os.MkdirAll(trimmed, 0o700); err != nil {
		return nil, fmt.Errorf("configclient: create cache dir: %w", err)
	}
	return &DirCache{Dir: trimmed}, nil
}

// pathFor 返回某 namespace 的缓存文件路径。
func (d *DirCache) pathFor(namespace string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, namespace)
	return filepath.Join(d.Dir, safe+".json")
}

// LoadNamespace 读取单个 namespace 缓存。
func (d *DirCache) LoadNamespace(_ context.Context, namespace string) (Snapshot, error) {
	raw, err := os.ReadFile(d.pathFor(namespace))
	if err != nil {
		return Snapshot{}, fmt.Errorf("configclient: read cache: %w", err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("configclient: decode cache: %w", err)
	}
	return snapshot, nil
}

// SaveNamespace 原子写入单个 namespace 缓存。
func (d *DirCache) SaveNamespace(_ context.Context, snapshot Snapshot) error {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("configclient: encode cache: %w", err)
	}
	path := d.pathFor(snapshot.Namespace)
	temp, err := os.CreateTemp(d.Dir, ".cfg-cache-*")
	if err != nil {
		return fmt.Errorf("configclient: create cache temp: %w", err)
	}
	tempName := temp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tempName)
		}
	}()
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		return fmt.Errorf("configclient: write cache temp: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("configclient: sync cache temp: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("configclient: close cache temp: %w", err)
	}
	if err := os.Chmod(tempName, 0o600); err != nil {
		return fmt.Errorf("configclient: chmod cache temp: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("configclient: rename cache: %w", err)
	}
	ok = true
	return nil
}

// Load 实现 SnapshotStore：读取目录中任意一个 json（兼容单文件 Store 接口时不使用）。
func (d *DirCache) Load(ctx context.Context) (Snapshot, error) {
	return Snapshot{}, fmt.Errorf("configclient: DirCache.Load requires LoadNamespace")
}

// Save 实现 SnapshotStore：按 snapshot.Namespace 写入。
func (d *DirCache) Save(ctx context.Context, snapshot Snapshot) error {
	return d.SaveNamespace(ctx, snapshot)
}
