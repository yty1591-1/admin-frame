package idempotency

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

// Fingerprint 计算请求指纹：method + path + rawQuery + body 的 SHA-256 hex。
// 用于检测「同键不同载荷」冲突。
func Fingerprint(method, path, rawQuery string, body []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(method))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(path))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(rawQuery))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// FingerprintRequest 从 HTTP 请求与已读 body 计算指纹。
func FingerprintRequest(r *http.Request, body []byte) string {
	if r == nil {
		return Fingerprint("", "", "", body)
	}
	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	return Fingerprint(r.Method, path, r.URL.RawQuery, body)
}
