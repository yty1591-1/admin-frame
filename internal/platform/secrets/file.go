package secrets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FileProvider 从目录读取密钥文件（仅 local / dev / test 与测试环境）。
//
// 目录布局：`<Dir>/<name>`，name 可含 `/` 形成子目录，例如 `db/dsn`。
// 文件名不允许越出 Dir（防目录穿越）；读取后会去掉行尾换行，
// 因为 `kubectl create secret` 生成的挂载文件常带尾随 `\n`。
type FileProvider struct {
	// Dir 密钥根目录。
	Dir string

	mu     sync.RWMutex
	closed bool
}

// NewFileProvider 构造文件 Provider。
func NewFileProvider(dir string) *FileProvider {
	return &FileProvider{Dir: dir}
}

// Get 读取密钥文件内容。
func (p *FileProvider) Get(_ context.Context, name string) (string, error) {
	raw, err := p.GetBytes(context.Background(), name)
	if err != nil {
		return "", err
	}
	value := strings.TrimRight(string(raw), "\r\n")
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return value, nil
}

// GetBytes 读取密钥文件原始字节。
func (p *FileProvider) GetBytes(_ context.Context, name string) ([]byte, error) {
	if err := p.checkClosed(); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(strings.TrimSpace(p.Dir))
	if err != nil {
		return nil, fmt.Errorf("secrets: resolve dir: %w", err)
	}
	cleaned := filepath.Clean(strings.TrimSpace(name))
	// 显式拒绝绝对路径与盘符：Windows 下 filepath.IsAbs("/etc/passwd") 为 false，
	// 仅靠 IsAbs 会把它当相对路径拼进根目录，跨平台语义不一致。
	if cleaned == "." ||
		strings.HasPrefix(cleaned, "..") ||
		strings.HasPrefix(cleaned, "/") ||
		strings.HasPrefix(cleaned, `\`) ||
		strings.Contains(cleaned, ":") ||
		filepath.IsAbs(cleaned) {
		return nil, fmt.Errorf("secrets: invalid key name %q", name)
	}
	path := filepath.Join(root, cleaned)
	// 二次校验：Join 后的路径必须仍在根目录内，防止 `a/../../etc/passwd` 变体。
	if !strings.HasPrefix(path, root+string(os.PathSeparator)) {
		return nil, fmt.Errorf("secrets: invalid key name %q", name)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: 路径已限制在配置的密钥根目录内。
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return nil, fmt.Errorf("secrets: read %q: %w", name, err)
	}
	return raw, nil
}

// Close 标记关闭；文件 Provider 无常驻句柄。
func (p *FileProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}

// checkClosed 在读路径上拒绝已关闭的 Provider。
func (p *FileProvider) checkClosed() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return ErrClosed
	}
	return nil
}
