// Package crypto 提供与业务无关的密码学原语：对称加解密、HMAC、Ed25519 签名、哈希与密钥包装。
//
// 设计边界（对齐 SEC）：
//   - 不得依赖 internal/；密钥须由调用方从环境变量 / KMS 注入，禁止硬编码
//   - 默认仅 AES-256-GCM（AEAD）、HMAC-SHA256、Ed25519；不提供 ECB/CBC 等易误用模式
//   - 密码哈希（bcrypt/argon2）见后续 pkg/password；JWT 见 pkg/jwt
//   - 错误与返回值不得包含密钥或明文；调用方日志须脱敏（SEC-05）
//
// 密文格式（Seal / WrapKey）：
//
//	version(1 byte)=0x01 || nonce(12) || ciphertext||tag
//
// 典型用法：
//
//	key := crypto.MustRandomBytes(crypto.AES256KeySize)
//	aead, err := crypto.NewAES256GCM(key)
//	blob, err := aead.Seal([]byte("payload"), []byte("aad"))
//	pt, err := aead.Open(blob, []byte("aad"))
package crypto
