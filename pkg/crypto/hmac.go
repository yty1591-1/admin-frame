package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
)

// HMACSHA256 使用 key 对 msg 计算 HMAC-SHA256；key 建议 ≥32 字节。
// 返回的 MAC 不得写入业务日志原文。
func HMACSHA256(key, msg []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("%w: empty hmac key", ErrInvalidKey)
	}
	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write(msg); err != nil {
		return nil, err
	}
	return mac.Sum(nil), nil
}

// HMACSHA256Verify 校验 mac 是否为 key 对 msg 的 HMAC-SHA256（常数时间比较）。
// 失败统一返回 ErrInvalidMAC，不区分「密钥错误」与「消息被篡改」。
func HMACSHA256Verify(key, msg, mac []byte) error {
	want, err := HMACSHA256(key, msg)
	if err != nil {
		return err
	}
	if !hmac.Equal(want, mac) {
		return ErrInvalidMAC
	}
	return nil
}
