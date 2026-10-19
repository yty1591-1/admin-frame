package crypto

import (
	"crypto/rand"
	"fmt"
	"io"
)

// RandomBytes 从 crypto/rand 读取 n 字节；n≤0 返回错误。
func RandomBytes(n int) ([]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("crypto: n must be > 0")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, fmt.Errorf("crypto: read random: %w", err)
	}
	return b, nil
}

// MustRandomBytes 同 RandomBytes，失败时 panic（仅用于进程启动确定性装配）。
func MustRandomBytes(n int) []byte {
	b, err := RandomBytes(n)
	if err != nil {
		panic(err)
	}
	return b
}

// ClearBytes 尽最大努力将 b 清零（便于密钥生命周期结束时擦除）。
// 不保证对抗编译器优化或内存转储；生产密钥仍应优先放在 KMS / OS 密钥库。
func ClearBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
