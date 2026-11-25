package domain

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"strings"
)

// SecretRule 算法约定（Generate / GenerateForBootstrap）。
const (
	SecretAlgorithmRandom    = ""            // 默认：随机字节按 Encoding 输出
	SecretAlgorithmRSAPEM    = "rsa-pem"     // RSA PKCS#8 PEM（SAML 等）
	SecretAlgorithmECP256PEM = "ec-p256-pem" //nolint:gosec // G101: 算法标识（EC P-256 PKCS#8 PEM），非凭据。
	SecretAlgorithmFCMSAJSON = "fcm-sa-json" //nolint:gosec // G101: 算法标识（FCM 服务账号 JSON 占位），非凭据。
)

// SecretRule 是敏感键的生成与格式约束（ADR-0014 决策 12）。
type SecretRule struct {
	MinLen       int
	MaxLen       int
	Encoding     string // base64url / hex / raw
	Algorithm    string // random（空）/ rsa-pem / ec-p256-pem / fcm-sa-json
	Prefix       string
	AutoGenerate bool
}

// Validate 校验规则自身。
func (r SecretRule) Validate() error {
	if r.MinLen < 0 || r.MaxLen < 0 || (r.MaxLen > 0 && r.MinLen > r.MaxLen) {
		return fmt.Errorf("configcenter: invalid secret_rule length bounds")
	}
	switch strings.ToLower(strings.TrimSpace(r.Encoding)) {
	case "", "base64url", "hex", "raw":
	default:
		return fmt.Errorf("configcenter: unsupported secret_rule encoding %q", r.Encoding)
	}
	switch strings.ToLower(strings.TrimSpace(r.Algorithm)) {
	case "", SecretAlgorithmRSAPEM, SecretAlgorithmECP256PEM, SecretAlgorithmFCMSAJSON:
	default:
		return fmt.Errorf("configcenter: unsupported secret_rule algorithm %q", r.Algorithm)
	}
	return nil
}

// Generate 按规则生成明文；仅 AutoGenerate=true 时可用。
func (r SecretRule) Generate() (string, error) {
	if !r.AutoGenerate {
		return "", fmt.Errorf("configcenter: secret_rule auto_generate is false")
	}
	return r.generatePlaintext()
}

// GenerateForBootstrap 生成播种占位明文，忽略 AutoGenerate 门闩（prod-like 全量占位）。
func (r SecretRule) GenerateForBootstrap() (string, error) {
	return r.generatePlaintext()
}

func (r SecretRule) generatePlaintext() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	switch strings.ToLower(strings.TrimSpace(r.Algorithm)) {
	case SecretAlgorithmRSAPEM:
		return generateRSAPEM()
	case SecretAlgorithmECP256PEM:
		return generateECP256PEM()
	case SecretAlgorithmFCMSAJSON:
		return generateFCMSAJSONPlaceholder()
	}

	length := r.MinLen
	if length <= 0 {
		length = 32
	}
	if r.MaxLen > 0 && length > r.MaxLen {
		length = r.MaxLen
	}
	raw := make([]byte, length)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	encoding := strings.ToLower(strings.TrimSpace(r.Encoding))
	if encoding == "" {
		encoding = "base64url"
	}
	// MinLen/MaxLen 始终指解码后字节数；不得按编码串字符数截断，
	// 否则 hex/base64 会破坏长度与 MatchPlaintext 对偶。
	var body string
	switch encoding {
	case "hex":
		body = hex.EncodeToString(raw)
	case "raw":
		body = string(raw)
		// raw 直接输出随机字节，可能整段都是空白/控制字符。这种值会在
		// CreateSecret 的 TrimSpace 处被判成空明文而报「plaintext is required」，
		// 使播种偶发失败（MinLen 越小概率越高）。重试到含可打印字符为止，
		// 不改变 raw 保留原始字节的语义（见 TestSecretRuleRawPreservesWhitespaceBytes）。
		for attempt := 0; attempt < 8 && strings.TrimSpace(body) == ""; attempt++ {
			if _, err := io.ReadFull(rand.Reader, raw); err != nil {
				return "", err
			}
			body = string(raw)
		}
	default:
		body = base64.RawURLEncoding.EncodeToString(raw)
	}
	return r.Prefix + body, nil
}

func generateRSAPEM() (string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

func generateECP256PEM() (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

func generateFCMSAJSONPlaceholder() (string, error) {
	// 格式合规占位：不能向 FCM 真实投递；运维须轮换为真实服务账号。
	nonce := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	id := hex.EncodeToString(nonce)
	return fmt.Sprintf(
		`{"type":"service_account","project_id":"bootstrap-placeholder","private_key_id":%q,"private_key":"-----BEGIN PRIVATE KEY-----\nPLACEHOLDER\n-----END PRIVATE KEY-----\n","client_email":"bootstrap-%s@placeholder.iam.gserviceaccount.com","client_id":"0","auth_uri":"https://accounts.google.com/o/oauth2/auth","token_uri":"https://oauth2.googleapis.com/token"}`,
		id, id,
	), nil
}

// MatchPlaintext 校验明文是否满足规则（CC-R-14：Create/Rotate 入库前）。
func (r SecretRule) MatchPlaintext(value string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	encoding := strings.ToLower(strings.TrimSpace(r.Encoding))
	if encoding == "" {
		encoding = "base64url"
	}
	// raw 是不透明字节序列：禁止 TrimSpace，否则自动生成的首尾空白字节会被截掉
	// （冒烟曾出现 length 31 < min 32）。hex/base64url 仍 trim，方便粘贴。
	raw := value
	if encoding != "raw" {
		raw = strings.TrimSpace(value)
	}
	if raw == "" {
		return fmt.Errorf("configcenter: secret plaintext is empty")
	}
	prefix := r.Prefix
	if prefix != "" {
		if !strings.HasPrefix(raw, prefix) {
			return fmt.Errorf("configcenter: secret plaintext missing required prefix")
		}
		raw = strings.TrimPrefix(raw, prefix)
	}
	var decoded []byte
	switch encoding {
	case "hex":
		b, err := hex.DecodeString(raw)
		if err != nil {
			return fmt.Errorf("configcenter: secret plaintext is not valid hex")
		}
		decoded = b
	case "raw":
		decoded = []byte(raw)
	default:
		b, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			// 兼容带 padding 的标准 base64url。
			b, err = base64.URLEncoding.DecodeString(raw)
			if err != nil {
				return fmt.Errorf("configcenter: secret plaintext is not valid base64url")
			}
		}
		decoded = b
	}
	n := len(decoded)
	if r.MinLen > 0 && n < r.MinLen {
		return fmt.Errorf("configcenter: secret plaintext length %d is below min %d", n, r.MinLen)
	}
	if r.MaxLen > 0 && n > r.MaxLen {
		return fmt.Errorf("configcenter: secret plaintext length %d exceeds max %d", n, r.MaxLen)
	}
	return nil
}
