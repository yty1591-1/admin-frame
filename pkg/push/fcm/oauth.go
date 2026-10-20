package fcm

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/httpclient"
	"github.com/admin-frame/admin-frame/pkg/push"
)

const (
	firebaseScope         = "https://www.googleapis.com/auth/firebase.messaging"
	providerTokenLifetime = time.Hour
	maxTokenResponseBytes = 64 << 10
)

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

type oauthTokenProvider struct {
	email      string
	key        *rsa.PrivateKey
	tokenURI   string
	httpClient *httpclient.Client
	clock      clock.Clock

	mu      sync.Mutex
	cached  string
	expires time.Time
}

func newOAuthTokenProvider(
	email string,
	key *rsa.PrivateKey,
	tokenURI string,
	client *httpclient.Client,
	clk clock.Clock,
) *oauthTokenProvider {
	return &oauthTokenProvider{email: email, key: key, tokenURI: tokenURI, httpClient: client, clock: clk}
}

func (p *oauthTokenProvider) token(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock.Now()
	if p.cached != "" && now.Before(p.expires) {
		return p.cached, nil
	}
	accessToken, expiresIn, err := p.fetch(ctx, now)
	if err != nil {
		return "", err
	}
	lifetime := time.Duration(expiresIn) * time.Second
	margin := time.Minute
	if lifetime <= 2*margin {
		margin = lifetime / 2
	}
	p.cached = accessToken
	p.expires = now.Add(lifetime - margin)
	return accessToken, nil
}

func (p *oauthTokenProvider) invalidate() {
	p.mu.Lock()
	p.cached = ""
	p.expires = time.Time{}
	p.mu.Unlock()
}

func (p *oauthTokenProvider) fetch(ctx context.Context, now time.Time) (token string, expiresIn int64, err error) {
	assertion, err := p.signJWT(now)
	if err != nil {
		return "", 0, push.NewProviderError("oauth", "sign", 0, pkgerrors.Internal("fcm: sign assertion"))
	}
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, push.NewProviderError("oauth", "request", 0, pkgerrors.Internal("fcm: build token request"))
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", 0, push.MapContextError("oauth", ctxErr)
		}
		return "", 0, push.NewProviderError("oauth", "transport", 0, pkgerrors.Unavailable("fcm: token transport error"))
	}
	defer func() { _ = resp.Body.Close() }() // 只读响应体，关闭错误不影响结果。
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponseBytes))
	if err != nil {
		return "", 0, push.NewProviderError("oauth", "read", 0, pkgerrors.Unavailable("fcm: read token response"))
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, mapTokenError(resp.StatusCode)
	}
	var payload tokenResponse
	if err := json.Unmarshal(body, &payload); err != nil || payload.AccessToken == "" {
		return "", 0, push.NewProviderError("oauth", "decode", 0, pkgerrors.Internal("fcm: decode token response"))
	}
	if payload.ExpiresIn <= 0 {
		payload.ExpiresIn = int64(providerTokenLifetime / time.Second)
	}
	return payload.AccessToken, payload.ExpiresIn, nil
}

func (p *oauthTokenProvider) signJWT(now time.Time) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iss":   p.email,
		"scope": firebaseScope,
		"aud":   p.tokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(providerTokenLifetime).Unix(),
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func mapTokenError(status int) error {
	switch {
	case status == http.StatusTooManyRequests:
		return push.NewProviderError("oauth", fmt.Sprint(status), 0,
			pkgerrors.ResourceExhausted("fcm: token endpoint rate limited"))
	case status >= http.StatusInternalServerError:
		return push.NewProviderError("oauth", fmt.Sprint(status), 0,
			pkgerrors.Unavailable("fcm: token endpoint unavailable"))
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return push.NewProviderError("oauth", fmt.Sprint(status), 0,
			pkgerrors.PermissionDenied("fcm: service account rejected"))
	default:
		return push.NewProviderError("oauth", fmt.Sprint(status), 0,
			pkgerrors.FailedPrecondition("fcm: token request rejected"))
	}
}
