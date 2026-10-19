package crypto

import "fmt"

// WrapKey 用 KEK（AES-256）包装 DEK，产出可安全落库/传输的密文。
//
// DEK 长度须在 16～64 字节（常见 16/24/32）；KEK 须为 32 字节。
// 实现为 AES-256-GCM 信封（非 RFC 3394 AES-KW），便于与 Seal 统一格式。
func WrapKey(kek, dek []byte) ([]byte, error) {
	if len(kek) != AES256KeySize {
		return nil, fmt.Errorf("%w: kek must be %d bytes", ErrInvalidKey, AES256KeySize)
	}
	if len(dek) < 16 || len(dek) > 64 {
		return nil, fmt.Errorf("%w: dek length must be in [16,64]", ErrInvalidKey)
	}
	aead, err := NewAES256GCM(kek)
	if err != nil {
		return nil, err
	}
	defer aead.Close()
	// AAD 固定标记，防止密文被挪作普通 Seal 载荷误解。
	return aead.Seal(dek, []byte("pkg/crypto:wrap:v1"))
}

// UnwrapKey 解开 WrapKey 产物，返回 DEK 副本；调用方用毕应 ClearBytes。
func UnwrapKey(kek, wrapped []byte) ([]byte, error) {
	if len(kek) != AES256KeySize {
		return nil, fmt.Errorf("%w: kek must be %d bytes", ErrInvalidKey, AES256KeySize)
	}
	aead, err := NewAES256GCM(kek)
	if err != nil {
		return nil, err
	}
	defer aead.Close()
	dek, err := aead.Open(wrapped, []byte("pkg/crypto:wrap:v1"))
	if err != nil {
		return nil, err
	}
	if len(dek) < 16 || len(dek) > 64 {
		ClearBytes(dek)
		return nil, ErrInvalidCiphertext
	}
	return dek, nil
}
