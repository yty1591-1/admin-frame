// Package saml 适配 crewjam/saml：把协议细节收敛为一个 SAMLProvider 端口实现。
package saml

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	crewjam "github.com/crewjam/saml"

	identityapp "github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/httpclient"
)

// SPConfig 是本系统（SP）的协议参数。
type SPConfig struct {
	EntityID        string
	AcsURL          string
	CertificatePEM  string
	PrivateKeyPEM   string
	CertificateNext string
	PrivateKeyNext  string
	// NameIDFormat AuthnRequest 请求的 NameID 格式；空值按 persistent 处理。
	NameIDFormat string
}

// Config 是单个 IdP 提供方配置。
type Config struct {
	Name         string
	IDPEntityID  string
	MetadataURL  string
	MetadataXML  string
	MetadataTTL  time.Duration
	MinRefresh   time.Duration
	Timeout      time.Duration
	ClockSkew    time.Duration
	AssertionTTL time.Duration
	SP           SPConfig
}

// Provider 实现 app.SAMLProvider；持有 active / next 两套 SP 以支持密钥轮换。
type Provider struct {
	name         string
	acsURL       url.URL
	sp           *crewjam.ServiceProvider
	spNext       *crewjam.ServiceProvider
	metadata     *MetadataSource
	clockSkew    time.Duration
	assertionTTL time.Duration

	// parseMu 串行化解析：IDPMetadata 会在刷新时替换，避免并发读写竞态。
	parseMu sync.Mutex
}

// NewProvider 构造 IdP 适配器；配置或元数据不可用时返回错误（失败关闭）。
func NewProvider(ctx context.Context, cfg Config) (*Provider, error) {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return nil, errors.New("saml: provider name is required")
	}
	acsURL, err := url.Parse(strings.TrimSpace(cfg.SP.AcsURL))
	if err != nil || acsURL.String() == "" {
		return nil, fmt.Errorf("saml: invalid acs_url: %w", err)
	}
	entityID := strings.TrimSpace(cfg.SP.EntityID)
	if entityID == "" {
		return nil, errors.New("saml: sp entity_id is required")
	}
	nameIDFormat, err := ParseNameIDFormat(cfg.SP.NameIDFormat)
	if err != nil {
		return nil, err
	}
	primary, err := ParseKeyPair(cfg.SP.CertificatePEM, cfg.SP.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	client, err := httpclient.New(httpclient.Config{Timeout: timeout})
	if err != nil {
		return nil, fmt.Errorf("saml: metadata http client: %w", err)
	}
	metadata, err := NewMetadataSource(ctx, MetadataConfig{
		URL:        cfg.MetadataURL,
		XML:        cfg.MetadataXML,
		EntityID:   cfg.IDPEntityID,
		Timeout:    timeout,
		TTL:        cfg.MetadataTTL,
		MinRefresh: cfg.MinRefresh,
	}, client.HTTP())
	if err != nil {
		return nil, err
	}
	sp := newServiceProvider(entityID, acsURL, primary, nameIDFormat)
	if _, err := metadata.Get(ctx); err != nil {
		return nil, err
	}
	clockSkew := cfg.ClockSkew
	if clockSkew < 0 {
		clockSkew = 0
	}
	assertionTTL := cfg.AssertionTTL
	if assertionTTL <= 0 {
		assertionTTL = 5 * time.Minute
	}
	provider := &Provider{
		name:         name,
		acsURL:       *acsURL,
		sp:           sp,
		metadata:     metadata,
		clockSkew:    clockSkew,
		assertionTTL: assertionTTL,
	}
	if strings.TrimSpace(cfg.SP.CertificateNext) != "" && strings.TrimSpace(cfg.SP.PrivateKeyNext) != "" {
		next, nextErr := ParseKeyPair(cfg.SP.CertificateNext, cfg.SP.PrivateKeyNext)
		if nextErr != nil {
			return nil, nextErr
		}
		provider.spNext = newServiceProvider(entityID, acsURL, next, nameIDFormat)
	}
	return provider, nil
}

// ParseNameIDFormat 把配置值规整为 crewjam 常量；空值默认 persistent。
// 不使用 transient：它每次登录都会换主体，会导致 auto_provision 每次新建账号。
func ParseNameIDFormat(raw string) (crewjam.NameIDFormat, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "persistent":
		return crewjam.PersistentNameIDFormat, nil
	case "transient":
		return crewjam.TransientNameIDFormat, nil
	case "unspecified":
		return crewjam.UnspecifiedNameIDFormat, nil
	default:
		return "", fmt.Errorf("saml: invalid name_id_format %q (want persistent, transient or unspecified)", raw)
	}
}

// newServiceProvider 构造 crewjam SP；显式关闭 IdP-initiated。
func newServiceProvider(entityID string, acsURL *url.URL, keys KeyPair, nameIDFormat crewjam.NameIDFormat) *crewjam.ServiceProvider {
	return &crewjam.ServiceProvider{
		EntityID:          entityID,
		AcsURL:            *acsURL,
		Key:               keys.Key,
		Certificate:       keys.Certificate,
		AuthnNameIDFormat: nameIDFormat,
		AllowIDPInitiated: false,
		// MetadataURL 仅用于元数据生成时的 entityID 兜底，这里显式留空。
		MetadataURL: url.URL{},
	}
}

// Name 实现 app.SAMLProvider。
func (p *Provider) Name() string { return p.name }

// AuthorizationURL 生成 HTTP-Redirect 形式的 AuthnRequest 地址。
func (p *Provider) AuthorizationURL(requestID, relayState string) (string, error) {
	md, err := p.metadata.Get(context.Background())
	if err != nil {
		return "", apperr.Unavailable("saml idp metadata unavailable")
	}
	p.parseMu.Lock()
	defer p.parseMu.Unlock()
	p.sp.IDPMetadata = md
	ssoURL := p.sp.GetSSOBindingLocation(crewjam.HTTPRedirectBinding)
	if ssoURL == "" {
		return "", errors.New("saml: idp does not support http-redirect sso")
	}
	request, err := p.sp.MakeAuthenticationRequest(ssoURL, crewjam.HTTPRedirectBinding, crewjam.HTTPPostBinding)
	if err != nil {
		return "", fmt.Errorf("saml: build authn request: %w", err)
	}
	// 用外部 requestID 覆盖库内随机 ID，才能把 InResponseTo 与 state 绑定。
	request.ID = requestID
	redirectURL, err := request.Redirect(relayState, p.sp)
	if err != nil {
		return "", fmt.Errorf("saml: sign authn request: %w", err)
	}
	return redirectURL.String(), nil
}

// ParseResponse 校验签名、时间条件、Audience 与加密断言。
func (p *Provider) ParseResponse(ctx context.Context, samlResponse, expectedRequestID string) (identityapp.SAMLIdentity, error) {
	raw := strings.TrimSpace(samlResponse)
	if raw == "" {
		return identityapp.SAMLIdentity{}, apperr.Unauthorized("invalid saml assertion")
	}
	decoded, err := decodeSAMLResponse(raw)
	if err != nil {
		return identityapp.SAMLIdentity{}, apperr.Unauthorized("invalid saml assertion")
	}
	md, err := p.metadata.Get(ctx)
	if err != nil {
		return identityapp.SAMLIdentity{}, apperr.Unavailable("saml idp metadata unavailable")
	}
	p.parseMu.Lock()
	defer p.parseMu.Unlock()
	p.sp.IDPMetadata = md
	if p.spNext != nil {
		p.spNext.IDPMetadata = md
	}
	assertion, parseErr := p.sp.ParseXMLResponse(decoded, []string{expectedRequestID}, p.acsURL)
	if parseErr != nil && p.spNext != nil && bytes.Contains(decoded, []byte("EncryptedAssertion")) {
		// 新证书加密、旧私钥仍为 active 时，用 next 私钥重试解密。
		assertion, parseErr = p.spNext.ParseXMLResponse(decoded, []string{expectedRequestID}, p.acsURL)
	}
	if parseErr != nil || assertion == nil {
		return identityapp.SAMLIdentity{}, apperr.Unauthorized("invalid saml assertion")
	}
	if err := p.validateTimes(assertion); err != nil {
		return identityapp.SAMLIdentity{}, apperr.Unauthorized("invalid saml assertion")
	}
	if assertion.Subject == nil || assertion.Subject.NameID == nil {
		return identityapp.SAMLIdentity{}, apperr.Unauthorized("invalid saml assertion")
	}
	subject := strings.TrimSpace(assertion.Subject.NameID.Value)
	assertionID := strings.TrimSpace(assertion.ID)
	if subject == "" || assertionID == "" {
		return identityapp.SAMLIdentity{}, apperr.Unauthorized("invalid saml assertion")
	}
	return identityapp.SAMLIdentity{
		Subject:     subject,
		AssertionID: assertionID,
		Issuer:      strings.TrimSpace(assertion.Issuer.Value),
	}, nil
}

// validateTimes 在库校验之外追加有效期上限与时钟偏差策略。
func (p *Provider) validateTimes(assertion *crewjam.Assertion) error {
	now := time.Now().UTC()
	if assertion.Conditions == nil {
		return errors.New("saml: assertion conditions are required")
	}
	conditions := assertion.Conditions
	if !conditions.NotBefore.IsZero() && now.Add(p.clockSkew).Before(conditions.NotBefore) {
		return errors.New("saml: assertion is not valid yet")
	}
	if !conditions.NotOnOrAfter.IsZero() && now.Add(-p.clockSkew).After(conditions.NotOnOrAfter) {
		return errors.New("saml: assertion has expired")
	}
	if !conditions.NotBefore.IsZero() && !conditions.NotOnOrAfter.IsZero() &&
		conditions.NotOnOrAfter.Sub(conditions.NotBefore) > p.assertionTTL {
		return errors.New("saml: assertion lifetime exceeds limit")
	}
	if !assertion.IssueInstant.IsZero() && now.Add(-p.clockSkew).Sub(assertion.IssueInstant) > p.assertionTTL {
		return errors.New("saml: assertion issue time is too old")
	}
	return nil
}

// decodeSAMLResponse 兼容 base64 中的换行与空白（IdP 表单常见）。
func decodeSAMLResponse(raw string) ([]byte, error) {
	compact := strings.NewReplacer("\n", "", "\r", "", "\t", "", " ", "").Replace(raw)
	decoded, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

// SPMetadataXML 生成 SP 元数据；active 与 next 证书同时发布，便于 IdP 平滑轮换。
func SPMetadataXML(cfg SPConfig) ([]byte, error) {
	acsURL, err := url.Parse(strings.TrimSpace(cfg.AcsURL))
	if err != nil || acsURL.String() == "" {
		return nil, fmt.Errorf("saml: invalid acs_url: %w", err)
	}
	primary, err := ParseKeyPair(cfg.CertificatePEM, cfg.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	nameIDFormat, err := ParseNameIDFormat(cfg.NameIDFormat)
	if err != nil {
		return nil, err
	}
	sp := newServiceProvider(strings.TrimSpace(cfg.EntityID), acsURL, primary, nameIDFormat)
	descriptor := sp.Metadata()
	if nextPEM := strings.TrimSpace(cfg.CertificateNext); nextPEM != "" {
		nextCerts, nextErr := ParseCertificates(nextPEM)
		if nextErr != nil {
			return nil, nextErr
		}
		if len(descriptor.SPSSODescriptors) > 0 {
			ssod := &descriptor.SPSSODescriptors[0]
			for _, cert := range nextCerts {
				ssod.KeyDescriptors = append(ssod.KeyDescriptors,
					crewjam.KeyDescriptor{
						Use: "encryption",
						KeyInfo: crewjam.KeyInfo{
							X509Data: crewjam.X509Data{
								X509Certificates: []crewjam.X509Certificate{
									{Data: base64.StdEncoding.EncodeToString(cert.Raw)},
								},
							},
						},
						EncryptionMethods: []crewjam.EncryptionMethod{
							{Algorithm: "http://www.w3.org/2001/04/xmlenc#aes128-cbc"},
							{Algorithm: "http://www.w3.org/2001/04/xmlenc#aes192-cbc"},
							{Algorithm: "http://www.w3.org/2001/04/xmlenc#aes256-cbc"},
							{Algorithm: "http://www.w3.org/2001/04/xmlenc#rsa-oaep-mgf1p"},
						},
					})
			}
		}
	}
	raw, err := xml.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("saml: marshal sp metadata: %w", err)
	}
	return raw, nil
}
