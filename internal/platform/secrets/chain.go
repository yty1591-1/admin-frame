package secrets

import (
	"context"
	"errors"
	"fmt"
)

// Chain 按顺序尝试多个 Provider，第一个命中的值生效。
//
// 用途：迁移期「KMS 为主 + 文件兜底」的显式组合。顺序在装配期固定，
// 不在运行时按错误猜测来源，避免出现「KMS 故障时静默回落到本地文件」。
type Chain []Provider

// NewChain 构造组合 Provider；空列表返回错误。
func NewChain(providers ...Provider) (Chain, error) {
	chain := make(Chain, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			chain = append(chain, provider)
		}
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("secrets: chain requires at least one provider")
	}
	return chain, nil
}

// Get 依次尝试各 Provider；只有 ErrNotFound 才继续向下，其他错误立即返回。
func (c Chain) Get(ctx context.Context, name string) (string, error) {
	for _, provider := range c {
		value, err := provider.Get(ctx, name)
		if err == nil {
			return value, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return "", err
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNotFound, name)
}

// GetBytes 依次尝试各 Provider 的字节读取。
func (c Chain) GetBytes(ctx context.Context, name string) ([]byte, error) {
	for _, provider := range c {
		value, err := provider.GetBytes(ctx, name)
		if err == nil {
			return value, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
}

// Close 关闭链上全部 Provider，返回首个错误。
func (c Chain) Close() error {
	var firstErr error
	for _, provider := range c {
		if err := provider.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
