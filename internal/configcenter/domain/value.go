package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// jsonValid 是 encoding/json.Valid 的薄封装，便于统一错误文案。
func jsonValid(raw []byte) error {
	if !json.Valid(raw) {
		return fmt.Errorf("invalid JSON")
	}
	return nil
}

// HashValue 计算配置值的 SHA-256（小写十六进制），用于版本与快照校验。
func HashValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
