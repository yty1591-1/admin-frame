package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
)

// EnvProvider 从进程环境变量读取密钥（仅 local / dev / test）。
//
// 密钥名会被转换为环境变量名：`db/dsn` → `<PREFIX>DB_DSN`，
// 即大写、非字母数字替换为下划线、加统一前缀，避免与其他变量冲突。
// 生产环境禁止使用本实现（启动校验会拒绝），因为环境变量会出现在
// 容器描述、崩溃转储与部分监控探针中。
type EnvProvider struct {
	// Prefix 环境变量前缀，如 "ADMIN_FRAME_SECRET_"；空值表示不加前缀。
	Prefix string

	mu     sync.RWMutex
	closed bool
}

// NewEnvProvider 构造环境变量 Provider。
func NewEnvProvider(prefix string) *EnvProvider {
	return &EnvProvider{Prefix: prefix}
}

// EnvName 返回密钥名对应的环境变量名，供部署清单与排障对齐。
func (p *EnvProvider) EnvName(name string) string {
	var b strings.Builder
	b.WriteString(p.Prefix)
	for _, r := range strings.ToUpper(strings.TrimSpace(name)) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Get 读取环境变量；未设置或为空串都视为缺失。
func (p *EnvProvider) Get(_ context.Context, name string) (string, error) {
	if err := p.checkClosed(); err != nil {
		return "", err
	}
	value, ok := os.LookupEnv(p.EnvName(name))
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return value, nil
}

// GetBytes 读取环境变量并按字节返回。
func (p *EnvProvider) GetBytes(ctx context.Context, name string) ([]byte, error) {
	value, err := p.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	return []byte(value), nil
}

// Close 标记关闭；环境变量无底层资源需要释放。
func (p *EnvProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}

// checkClosed 在读路径上拒绝已关闭的 Provider。
func (p *EnvProvider) checkClosed() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return ErrClosed
	}
	return nil
}
