package oauth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// 默认 JWKS 缓存策略：15 分钟主动刷新；未知 kid 触发强制刷新，但至少间隔 1 分钟，
// 避免伪造 kid 造成对 IdP 的放大请求（P1-6 G6-3）。
const (
	defaultJWKSTTL              = 15 * time.Minute
	defaultJWKSMinRefreshWindow = time.Minute
)

// jwksConfig 是 JWKS 拉取与缓存参数；仅在 OIDC 模式下使用。
type jwksConfig struct {
	URL                string
	TTL                time.Duration
	MinRefreshInterval time.Duration
	Client             *http.Client
	Now                func() time.Time
	// Provider 是提供方名，仅用于指标标签。
	Provider string
	// Metrics 是刷新结果与缓存新鲜度指标出口；可为 nil。
	Metrics app.Metrics
}

type jwksDocument struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// jwks 缓存 IdP 的 RSA 公钥，支持密钥轮换与限频强制刷新。
type jwks struct {
	cfg         jwksConfig
	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
	// lastSuccess 是最后一次成功解析出可用公钥的时刻，用于暴露缓存新鲜度。
	lastSuccess time.Time
}

func newJWKS(cfg jwksConfig) (*jwks, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("oidc jwks url is required")
	}
	if cfg.TTL <= 0 {
		cfg.TTL = defaultJWKSTTL
	}
	if cfg.MinRefreshInterval <= 0 {
		cfg.MinRefreshInterval = defaultJWKSMinRefreshWindow
	}
	if cfg.MinRefreshInterval > cfg.TTL {
		cfg.MinRefreshInterval = cfg.TTL
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &jwks{cfg: cfg, keys: map[string]*rsa.PublicKey{}}, nil
}

// key 返回 kid 对应的 RSA 公钥；未知 kid 会限频强制刷新一次以支持轮换。
func (j *jwks) key(ctx context.Context, kid, alg string) (*rsa.PublicKey, error) {
	if alg != "RS256" {
		// 只接受 RS256，避免 alg 混淆与降级攻击。
		return nil, apperr.Unauthorized("id_token signing algorithm not allowed")
	}
	if strings.TrimSpace(kid) == "" {
		return nil, apperr.Unauthorized("id_token missing key id")
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	now := j.cfg.Now()
	if len(j.keys) == 0 || now.Sub(j.fetchedAt) >= j.cfg.TTL {
		// 刷新失败后同样受最小间隔约束，避免 IdP 故障期间每次校验都触发重试。
		if now.Sub(j.lastAttempt) >= j.cfg.MinRefreshInterval {
			if err := j.refresh(ctx, now); err != nil {
				// IdP 短时不可用时保留上一份缓存，避免已签发的合法 id_token 全部被拒；
				// 从未成功拉取过任何公钥时仍失败关闭。
				if len(j.keys) == 0 {
					return nil, err
				}
			}
		} else if len(j.keys) == 0 {
			return nil, apperr.Unavailable("oidc jwks unavailable")
		}
	}
	if key, ok := j.keys[kid]; ok {
		return key, nil
	}
	// 未命中可能是 IdP 刚轮换密钥；限频刷新后仍缺失则判定为非法令牌。
	if now.Sub(j.lastAttempt) >= j.cfg.MinRefreshInterval {
		if err := j.refresh(ctx, now); err != nil {
			return nil, err
		}
	}
	if key, ok := j.keys[kid]; ok {
		return key, nil
	}
	return nil, apperr.Unauthorized("id_token signing key not found")
}

// age 返回距上次成功拉取 JWKS 的时长；从未成功时 ok=false。
func (j *jwks) age() (time.Duration, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.lastSuccess.IsZero() {
		return 0, false
	}
	return j.cfg.Now().Sub(j.lastSuccess), true
}

// refresh 拉取并解析 JWKS；失败时保留旧缓存（若存在），由调用方决定是否接受。
func (j *jwks) refresh(ctx context.Context, now time.Time) (err error) {
	defer func() {
		if j.cfg.Metrics == nil {
			return
		}
		result := app.MetricResultSuccess
		if err != nil {
			result = app.MetricResultFailure
		}
		j.cfg.Metrics.ObserveJWKSRefresh(ctx, j.cfg.Provider, result)
	}()
	j.lastAttempt = now
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, j.cfg.URL, http.NoBody)
	if err != nil {
		return apperr.Unavailable("oidc jwks request build failed")
	}
	request.Header.Set("Accept", "application/json")
	response, err := j.cfg.Client.Do(request)
	if err != nil {
		return apperr.Unavailable("oidc jwks unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return apperr.Unavailable("oidc jwks unavailable")
	}
	if response.StatusCode >= http.StatusBadRequest {
		return apperr.Unavailable("oidc jwks unavailable")
	}

	var document jwksDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return apperr.Unavailable("oidc jwks invalid response")
	}
	keys := make(map[string]*rsa.PublicKey, len(document.Keys))
	for _, item := range document.Keys {
		key, err := parseRSAKey(item)
		if err != nil {
			continue
		}
		keys[item.Kid] = key
	}
	if len(keys) == 0 {
		// 旧缓存由调用方保留：本次刷新按失败计数，已签发的合法令牌仍可按旧公钥校验。
		return apperr.Unavailable("oidc jwks contains no usable signing key")
	}
	j.keys = keys
	j.fetchedAt = now
	j.lastSuccess = now
	return nil
}

// parseRSAKey 把 JWK 的 n/e 解析为 rsa.PublicKey；非 RSA 签名密钥直接忽略。
func parseRSAKey(item jwkKey) (*rsa.PublicKey, error) {
	if item.Kty != "RSA" || strings.TrimSpace(item.Kid) == "" {
		return nil, fmt.Errorf("unsupported jwk")
	}
	if item.Use != "" && item.Use != "sig" {
		return nil, fmt.Errorf("unsupported jwk use")
	}
	modulus, err := decodeBase64URL(item.N)
	if err != nil || len(modulus) == 0 {
		return nil, fmt.Errorf("invalid jwk modulus")
	}
	exponent, err := decodeBase64URL(item.E)
	if err != nil || len(exponent) == 0 {
		return nil, fmt.Errorf("invalid jwk exponent")
	}
	value := new(big.Int).SetBytes(exponent)
	if value.Sign() <= 0 || !value.IsInt64() {
		return nil, fmt.Errorf("invalid jwk exponent")
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(modulus),
		E: int(value.Int64()),
	}, nil
}

func decodeBase64URL(value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("empty base64url value")
	}
	return base64.RawURLEncoding.DecodeString(value)
}
