package crypto

import stderrors "errors"

var (
	// ErrInvalidKey 表示密钥长度或类型不合法。
	ErrInvalidKey = stderrors.New("crypto: invalid key")
	// ErrInvalidCiphertext 表示密文格式非法或认证失败（不区分细节，防侧信道）。
	ErrInvalidCiphertext = stderrors.New("crypto: invalid ciphertext")
	// ErrInvalidMAC 表示 HMAC 或数字签名校验失败。
	ErrInvalidMAC = stderrors.New("crypto: invalid mac")
	// ErrTooShort 表示输入长度不足。
	ErrTooShort = stderrors.New("crypto: input too short")
)

const (
	// AES256KeySize 是 AES-256 密钥字节数。
	AES256KeySize = 32
	// NonceSize 是 AES-GCM 推荐 nonce 长度。
	NonceSize = 12
	// blobVersion 当前密文版本前缀。
	blobVersion byte = 0x01
)
