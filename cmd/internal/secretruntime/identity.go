package secretruntime

import (
	"context"
	"fmt"
	"strings"

	identityconfig "github.com/admin-frame/admin-frame/internal/identity/config"
	"github.com/admin-frame/admin-frame/pkg/secretclient"
)

const (
	identityHashKeySecretName    = "identity.hash_key"         //nolint:gosec // G101: 配置中心密钥名
	identityMFASecretKeyName     = "identity.mfa.secret_key"   //nolint:gosec // G101: 配置中心密钥名
	identitySAMLPrivateKeyName   = "identity.saml.private_key" //nolint:gosec // G101: 配置中心密钥名
	identitySubjectKeySecretName = "identity.subject.key"      //nolint:gosec // G101: 配置中心密钥名
	identityNamespace            = "identity"
	identitySubjectActiveKeyID   = "v1"
	// IdentityHashKeyValueKey 是注入 appconfig Values 的扁平路径。
	IdentityHashKeyValueKey = "identity.hash_key"
	// IdentityMFASecretValueKey 是注入 appconfig Values 的扁平路径。
	IdentityMFASecretValueKey = "identity.mfa.secret_key"
	// IdentitySAMLPrivateKeyValueKey 是注入 appconfig Values 的扁平路径。
	IdentitySAMLPrivateKeyValueKey = "identity.saml.private_key"
)

// ResolveIdentityHashKey 从密钥子域解析 PII 摘要主密钥明文（fail-fast）。
func ResolveIdentityHashKey(ctx context.Context, env string, client *secretclient.Client) (string, error) {
	return resolveIdentitySecret(ctx, env, client, identityHashKeySecretName)
}

// ResolveIdentityMFASecretKey 从密钥子域解析 MFA 主密钥明文（fail-fast）。
func ResolveIdentityMFASecretKey(ctx context.Context, env string, client *secretclient.Client) (string, error) {
	return resolveIdentitySecret(ctx, env, client, identityMFASecretKeyName)
}

// ResolveIdentitySAMLPrivateKey 从密钥子域解析 SAML SP 私钥 PEM（fail-fast）。
func ResolveIdentitySAMLPrivateKey(ctx context.Context, env string, client *secretclient.Client) (string, error) {
	return resolveIdentitySecret(ctx, env, client, identitySAMLPrivateKeyName)
}

// ResolveIdentitySubjectKey 从密钥子域解析主体加密主密钥明文（fail-fast）。
func ResolveIdentitySubjectKey(ctx context.Context, env string, client *secretclient.Client) (string, error) {
	return resolveIdentitySecret(ctx, env, client, identitySubjectKeySecretName)
}

// ResolveIdentitySecretsFromEnv 一次构造 client，解析 hash_key 与 mfa.secret_key。
func ResolveIdentitySecretsFromEnv(ctx context.Context, env string) (hashKey, mfaSecret string, err error) {
	client, err := NewFromEnv()
	if err != nil {
		return "", "", err
	}
	hashKey, err = ResolveIdentityHashKey(ctx, env, client)
	if err != nil {
		return "", "", err
	}
	mfaSecret, err = ResolveIdentityMFASecretKey(ctx, env, client)
	if err != nil {
		return "", "", err
	}
	return hashKey, mfaSecret, nil
}

// ResolveIdentitySAMLPrivateKeyFromEnv 构造 client 并解析 SAML 私钥。
//
// SAML 未启用时调用方可忽略错误；启用时由 Validate 兜底缺钥。
func ResolveIdentitySAMLPrivateKeyFromEnv(ctx context.Context, env string) (string, error) {
	client, err := NewFromEnv()
	if err != nil {
		return "", err
	}
	return ResolveIdentitySAMLPrivateKey(ctx, env, client)
}

// ResolveIdentitySubjectKeyFromEnv 构造 client 并解析主体加密主密钥。
//
// 未配置主体加密时调用方可忽略错误；需要加密 PII 时由 Validate 兜底缺钥。
func ResolveIdentitySubjectKeyFromEnv(ctx context.Context, env string) (string, error) {
	client, err := NewFromEnv()
	if err != nil {
		return "", err
	}
	return ResolveIdentitySubjectKey(ctx, env, client)
}

// ApplyIdentitySubjectKey 在 Subject.Keys 为空时写入单槽密钥环（CC-R-05）。
//
// 多版本 previous / mfa.keys 环仍走 STANDARDS §3.1.1 永久排除；本函数只托管当前 active 槽。
func ApplyIdentitySubjectKey(cfg *identityconfig.IdentityConfig, plaintext string) {
	if cfg == nil {
		return
	}
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" || len(cfg.Subject.Keys) > 0 {
		return
	}
	cfg.Subject.ActiveKeyID = identitySubjectActiveKeyID
	cfg.Subject.Keys = []identityconfig.IdentityMFAKeyConfig{{
		ID:  identitySubjectActiveKeyID,
		Key: plaintext,
	}}
}

func resolveIdentitySecret(ctx context.Context, env string, client *secretclient.Client, name string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("secretruntime: secretclient is required for %s", name)
	}
	resolvedEnv := strings.TrimSpace(env)
	if resolvedEnv == "" {
		resolvedEnv = "dev"
	}
	value, err := client.Resolve(ctx, resolvedEnv, identityNamespace, name)
	if err != nil {
		return "", fmt.Errorf("secretruntime: resolve %s: %w", name, err)
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("secretruntime: %s resolved empty", name)
	}
	return value, nil
}
