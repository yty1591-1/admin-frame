// Package secrets 提供密钥读取的统一抽象（ENT-07 T1）。
//
// 目标：把「密钥从哪来」与「业务怎么用」分开。生产由 Vault / 云 KMS 适配实现
// Provider，本地用 env / file 实现；业务与配置只依赖 Provider 接口，
// 切换来源不需要改业务代码，也不会出现「生产回落到环境变量」的隐式路径。
//
// 约束（SECURITY_STANDARDS §5 / ARCHITECTURE §10.3 / ENT-07）：
//   - Provider 未就绪时启动必须失败，不允许空值继续运行；
//   - 密钥值禁止出现在日志、错误信息与审计载荷中（只允许出现密钥名）；
//   - env / file 实现仅适用于 local / dev / test，prod 由配置校验拒绝。
package secrets

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// 常见错误。
var (
	// ErrNotFound 表示 Provider 中不存在该密钥名。
	ErrNotFound = errors.New("secrets: key not found")
	// ErrClosed 表示 Provider 已关闭，不能再读取。
	ErrClosed = errors.New("secrets: provider closed")
)

// Provider 是密钥读取契约。
//
// 实现须保证：
//   - 读取失败返回 ErrNotFound 或包装后的错误，不返回空串冒充成功；
//   - 返回值不写日志；调用方拿到后自行使用，不落库、不回显。
type Provider interface {
	// Get 返回密钥的字符串值。
	Get(ctx context.Context, name string) (string, error)
	// GetBytes 返回密钥的字节值（证书、DER、密钥文件等二进制材料）。
	GetBytes(ctx context.Context, name string) ([]byte, error)
	// Close 释放底层连接与缓存；可重复调用。
	Close() error
}

// Resolver 是 Provider 的批量读取辅助，用于启动期一次性拉取所需密钥。
type Resolver struct {
	provider Provider
}

// NewResolver 构造批量读取器；provider 为 nil 时返回错误（启动期必须显式注入）。
func NewResolver(provider Provider) (*Resolver, error) {
	if provider == nil {
		return nil, fmt.Errorf("secrets: provider is required")
	}
	return &Resolver{provider: provider}, nil
}

// Require 启动期读取全部必需密钥；任一缺失即返回错误，阻止进程启动。
//
// 错误信息只包含密钥名，不包含密钥值。
func (r *Resolver) Require(ctx context.Context, names ...string) (map[string]string, error) {
	if len(names) == 0 {
		return map[string]string{}, nil
	}
	values := make(map[string]string, len(names))
	missing := make([]string, 0, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return nil, fmt.Errorf("secrets: empty key name")
		}
		value, err := r.provider.Get(ctx, trimmed)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				missing = append(missing, trimmed)
				continue
			}
			return nil, fmt.Errorf("secrets: load %q: %w", trimmed, err)
		}
		values[trimmed] = value
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("secrets: missing required keys: %s", strings.Join(missing, ", "))
	}
	return values, nil
}
