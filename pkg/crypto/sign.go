package crypto

import (
	"crypto/ed25519"
	"fmt"
)

// GenerateEd25519 生成 Ed25519 密钥对；私钥须妥善保管，不得入仓或打日志。
func GenerateEd25519() (publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey, err error) {
	return ed25519.GenerateKey(nil)
}

// SignEd25519 使用私钥对 msg 签名。
func SignEd25519(privateKey ed25519.PrivateKey, msg []byte) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: ed25519 private key size", ErrInvalidKey)
	}
	return ed25519.Sign(privateKey, msg), nil
}

// VerifyEd25519 校验签名；失败返回 ErrInvalidMAC（语义为「认证失败」）。
func VerifyEd25519(publicKey ed25519.PublicKey, msg, sig []byte) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: ed25519 public key size", ErrInvalidKey)
	}
	if !ed25519.Verify(publicKey, msg, sig) {
		return ErrInvalidMAC
	}
	return nil
}
