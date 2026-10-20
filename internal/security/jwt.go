package security

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/jwt"
)

// JWT 签名算法（jwt.alg）。
const (
	// JWTAlgHS256 对称密钥（适合单服务内网；密钥须 ≥32 字节）。
	JWTAlgHS256 = "HS256"
	// JWTAlgRS256 非对称密钥（推荐；API 进程只持有公钥）。
	JWTAlgRS256 = "RS256"
)

// minHMACSecretBytes 是 HS256 密钥的最小长度（与 pkg/jwt 保持一致）。
const minHMACSecretBytes = 32

// verifyTTL 仅用于满足 pkg/jwt 构造约束：API 进程只校验、不签发。
const verifyTTL = time.Minute

// JWTConfig 是 JWT 鉴权的装配参数（yaml: auth.jwt）。
//
// 密钥与私钥不写入配置文件：Secret / Secrets 走环境变量
// （ADMIN_FRAME_AUTH_JWT_SECRET 等），公钥走文件挂载。
type JWTConfig struct {
	// Alg 签名算法：HS256（默认）或 RS256。
	Alg string `yaml:"alg"`
	// Issuer 期望的 iss；必填。
	Issuer string `yaml:"issuer"`
	// Audience 期望的 aud 列表；必填且须包含进程 channel。
	Audience []string `yaml:"audience"`
	// Leeway 校验时钟偏移容忍；默认 0。
	Leeway time.Duration `yaml:"leeway"`
	// Secret 当前 HMAC 密钥；仅 HS256，长度须 ≥32 字节。
	Secret string `yaml:"secret"`
	// Secrets 额外可接受的旧密钥（轮换期只校验）。
	Secrets []string `yaml:"secrets"`
	// PublicKeyFile RSA 公钥文件（PEM，PKIX 或 PKCS#1）；仅 RS256。
	PublicKeyFile string `yaml:"public_key_file"`
	// PublicKeysFile 轮换期额外公钥文件（可含多个 PEM 块）；仅 RS256。
	PublicKeysFile string `yaml:"public_keys_file"`
	// Clock 可注入时钟（测试用）；yaml:"-" 不参与配置绑定。
	Clock jwt.Clock `yaml:"-"`
}

// Validate 校验 JWT 配置与进程 channel 的一致性（CFG-03 / SEC-04）。
//
// 错误文本只包含字段路径，不回显密钥取值。
func (c JWTConfig) Validate(channel string) error {
	var errs []error
	alg := c.normalizedAlg()
	switch alg {
	case JWTAlgHS256, JWTAlgRS256:
	default:
		errs = append(errs, fmt.Errorf("auth.jwt.alg unsupported: %s", c.Alg))
	}
	if strings.TrimSpace(c.Issuer) == "" {
		errs = append(errs, fmt.Errorf("auth.jwt.issuer is required when auth.mode=jwt"))
	}
	if len(c.Audience) == 0 {
		errs = append(errs, fmt.Errorf("auth.jwt.audience is required when auth.mode=jwt"))
	} else if !containsFold(c.Audience, channel) {
		errs = append(errs, fmt.Errorf("auth.jwt.audience must contain channel %q", channel))
	}
	if c.Leeway < 0 {
		errs = append(errs, fmt.Errorf("auth.jwt.leeway must be >= 0"))
	}
	switch alg {
	case JWTAlgHS256:
		if err := c.validateHMACKeys(); err != nil {
			errs = append(errs, err)
		}
	case JWTAlgRS256:
		if strings.TrimSpace(c.PublicKeyFile) == "" && strings.TrimSpace(c.PublicKeysFile) == "" {
			errs = append(errs, fmt.Errorf("auth.jwt.public_key_file is required when alg=RS256"))
		}
	}
	return errors.Join(errs...)
}

// normalizedAlg 返回规范化后的算法名；空值默认 HS256。
func (c JWTConfig) normalizedAlg() string {
	alg := strings.ToUpper(strings.TrimSpace(c.Alg))
	if alg == "" {
		return JWTAlgHS256
	}
	return alg
}

// validateHMACKeys 校验当前密钥与轮换密钥均满足长度要求。
func (c JWTConfig) validateHMACKeys() error {
	if len(c.Secret) < minHMACSecretBytes {
		return fmt.Errorf("auth.jwt.secret must be at least %d bytes", minHMACSecretBytes)
	}
	for i, s := range c.Secrets {
		if len(s) < minHMACSecretBytes {
			return fmt.Errorf("auth.jwt.secrets[%d] must be at least %d bytes", i, minHMACSecretBytes)
		}
	}
	return nil
}

// containsFold 报告列表是否包含目标值（大小写不敏感）。
func containsFold(items []string, target string) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), target) {
			return true
		}
	}
	return false
}

// NewJWTAuth 构造 JWT 鉴权中间件；配置非法时返回 error（启动期失败）。
//
// 校验范围：签名、exp/nbf（含 leeway）、iss、aud。成功后把 claims 映射为
// auth.Principal 并写入 contextx（sub → SubjectID，merchant_id / roles 可选）。
func NewJWTAuth(cfg JWTConfig, expected auth.Channel) (func(http.Handler) http.Handler, error) {
	if err := cfg.Validate(string(expected)); err != nil {
		return nil, err
	}
	mgr, err := cfg.manager()
	if err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			traceID := httpx.TraceIDFromContext(r.Context())
			token, ok := bearerToken(r)
			if !ok {
				httpx.FromError(w, apperr.Unauthorized("missing bearer token"), traceID)
				return
			}
			claims, err := mgr.Parse(token)
			if err != nil {
				// 不回显具体校验原因，避免给攻击者提供枚举信息（SEC-05）。
				httpx.FromError(w, apperr.Unauthorized("invalid token"), traceID)
				return
			}
			p, err := principalFromClaims(claims, expected)
			if err != nil {
				httpx.FromError(w, apperr.Unauthorized("invalid token"), traceID)
				return
			}
			p.RequestID = traceID
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
		})
	}, nil
}

// manager 按算法构造 pkg/jwt 校验器。
func (c JWTConfig) manager() (*jwt.Manager, error) {
	if c.normalizedAlg() == JWTAlgRS256 {
		return c.rsaManager()
	}
	return c.hmacManager()
}

// hmacManager 构造 HS256 校验器（含轮换密钥）。
func (c JWTConfig) hmacManager() (*jwt.Manager, error) {
	alts := make([][]byte, 0, len(c.Secrets))
	for _, s := range c.Secrets {
		alts = append(alts, []byte(s))
	}
	return jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   c.Issuer,
		Audience: c.Audience,
		Secret:   []byte(c.Secret),
		Secrets:  alts,
		// API 进程只校验不签发；verifyTTL 仅满足构造函数对 TTL>0 的要求。
		TTL:    verifyTTL,
		Leeway: c.Leeway,
		Clock:  c.Clock,
	})
}

// rsaManager 构造 RS256 校验器（含轮换公钥）。
func (c JWTConfig) rsaManager() (*jwt.Manager, error) {
	pub, err := loadPublicKeys(c.PublicKeyFile)
	if err != nil {
		return nil, err
	}
	if len(pub) == 0 {
		return nil, fmt.Errorf("auth.jwt.public_key_file: no public key found")
	}
	var alts []*rsa.PublicKey
	if strings.TrimSpace(c.PublicKeysFile) != "" {
		alts, err = loadPublicKeys(c.PublicKeysFile)
		if err != nil {
			return nil, err
		}
	}
	return jwt.NewRSA(jwt.RSAConfig{
		Issuer:     c.Issuer,
		Audience:   c.Audience,
		PublicKey:  pub[0],
		PublicKeys: alts,
		TTL:        verifyTTL,
		Leeway:     c.Leeway,
		Clock:      c.Clock,
	})
}

// loadPublicKeys 读取 PEM 文件中的全部 RSA 公钥（PKIX 或 PKCS#1）。
func loadPublicKeys(path string) ([]*rsa.PublicKey, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- 路径来自运维配置，非请求输入。
	if err != nil {
		return nil, fmt.Errorf("read public key file: %w", err)
	}
	var keys []*rsa.PublicKey
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		key, err := parseRSAPublicKey(block)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// parseRSAPublicKey 解析单个 PEM 块；不支持的类型返回错误。
func parseRSAPublicKey(block *pem.Block) (*rsa.PublicKey, error) {
	switch block.Type {
	case "PUBLIC KEY":
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse public key: %w", err)
		}
		key, ok := parsed.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("public key is not RSA")
		}
		return key, nil
	case "RSA PUBLIC KEY":
		key, err := x509.ParsePKCS1PublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse rsa public key: %w", err)
		}
		return key, nil
	case "CERTIFICATE":
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse certificate: %w", err)
		}
		key, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("certificate public key is not RSA")
		}
		return key, nil
	default:
		return nil, fmt.Errorf("unsupported pem block %q", block.Type)
	}
}

// principalFromClaims 把 JWT 声明映射为统一 Principal。
//
// 校验约束：sub 必填；channel 声明若存在须与进程端别一致（防跨端冒用，SEC-03）。
func principalFromClaims(claims jwt.Claims, expected auth.Channel) (auth.Principal, error) {
	subject := strings.TrimSpace(claims.Subject)
	if subject == "" {
		return auth.Principal{}, apperr.ErrUnauthorized
	}
	if ch := claims.GetString("channel"); ch != "" && !strings.EqualFold(ch, string(expected)) {
		return auth.Principal{}, apperr.ErrUnauthorized
	}
	return auth.Principal{
		Channel:    expected,
		SubjectID:  subject,
		MerchantID: claims.GetString("merchant_id"),
		Roles:      claims.GetStringSlice("roles"),
	}, nil
}
