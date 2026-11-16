package configcenterruntime

import (
	"crypto/sha256"
	"encoding/hex"
)

// hashValueForTest 与快照契约一致：SHA-256 十六进制。
func hashValueForTest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
