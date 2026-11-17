package secretruntime

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// OAuthClientSecretName 返回 OAuth provider 在密钥子域中的规范名（CC-R-05）。
//
// 形态：identity.oauth.<provider>.client_secret；provider 规范化为小写 [a-z0-9._-]。
func OAuthClientSecretName(provider string) string {
	return "identity.oauth." + sanitizeSecretNameSegment(provider) + ".client_secret"
}

// ResolveOAuthClientSecretOrEnv 优先 secretclient，失败时仅 local/dev/test 回退裸 env。
func ResolveOAuthClientSecretOrEnv(ctx context.Context, env, provider, envVar string) (string, error) {
	name := OAuthClientSecretName(provider)
	value, err := resolveIdentitySecretFromEnv(ctx, env, name)
	if err == nil {
		return value, nil
	}
	if bareErr := RejectBareSecretEnv(env, envVar); bareErr != nil {
		return "", fmt.Errorf("%s: resolve failed (%v); %w", name, err, bareErr)
	}
	fallback := strings.TrimSpace(os.Getenv(strings.TrimSpace(envVar)))
	if fallback == "" {
		return "", fmt.Errorf("%s: resolve failed (%v) and environment variable %s is empty",
			name, err, envVar)
	}
	return fallback, nil
}

func resolveIdentitySecretFromEnv(ctx context.Context, env, name string) (string, error) {
	client, err := NewFromEnv()
	if err != nil {
		return "", err
	}
	return resolveIdentitySecret(ctx, env, client, name)
}

func sanitizeSecretNameSegment(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "unnamed"
	}
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "unnamed"
	}
	return out
}
