package configclient

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SnapshotStore 持久化已验签快照，供中心 / DB 不可用时冷启动兜底（CC-F-37）。
type SnapshotStore interface {
	Load(ctx context.Context) (Snapshot, error)
	Save(ctx context.Context, snapshot Snapshot) error
}

// FileStore 把快照写到进程本地文件。
//
// 写入采用「临时文件 + fsync + rename」原子替换，权限 0600；
// 文件只含非敏感取值，敏感项只有 value_hash。
type FileStore struct {
	// Path 是快照文件绝对路径。
	Path string
}

// NewFileStore 构造文件快照仓库；路径必填。
func NewFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, fmt.Errorf("configclient: snapshot file path is required")
	}
	return &FileStore{Path: path}, nil
}

// Load 读取并解码快照；文件不存在返回 os.ErrNotExist 包装错误。
func (f *FileStore) Load(_ context.Context) (Snapshot, error) {
	raw, err := os.ReadFile(f.Path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("configclient: read snapshot file: %w", err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("configclient: decode snapshot file: %w", err)
	}
	return snapshot, nil
}

// Save 原子写入快照；目录不存在时按 0700 创建。
func (f *FileStore) Save(_ context.Context, snapshot Snapshot) error {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("configclient: encode snapshot file: %w", err)
	}
	dir := filepath.Dir(f.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("configclient: create snapshot dir: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".config-snapshot-*")
	if err != nil {
		return fmt.Errorf("configclient: create snapshot temp file: %w", err)
	}
	tempName := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}()

	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("configclient: chmod snapshot temp file: %w", err)
	}
	if _, err := temp.Write(raw); err != nil {
		return fmt.Errorf("configclient: write snapshot temp file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("configclient: sync snapshot temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("configclient: close snapshot temp file: %w", err)
	}
	if err := os.Rename(tempName, f.Path); err != nil {
		return fmt.Errorf("configclient: replace snapshot file: %w", err)
	}
	return nil
}
