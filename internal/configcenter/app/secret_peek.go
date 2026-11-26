package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/envelope"
)

// PeekActivePlaintext 供安装态连通性探测读取活跃明文；不做 grant 校验
// （调用方为 config-api 本进程）。找不到或未激活返回 NotFound。
func (s *SecretService) PeekActivePlaintext(ctx context.Context, env, namespace, name string) (string, error) {
	if s == nil || s.secrets == nil || s.kek == nil {
		return "", apperr.Internal("secret service unavailable", nil)
	}
	secret, err := s.secrets.GetByName(ctx, env, namespace, name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", apperr.NotFound(fmt.Sprintf("secret %s/%s missing", namespace, name))
		}
		return "", apperr.Internal("lookup secret failed", err)
	}
	if secret.Status != domain.SecretStatusActive || secret.ActiveVersion == nil {
		return "", apperr.NotFound(fmt.Sprintf("secret %s/%s is not active", namespace, name))
	}
	version, err := s.secrets.GetVersion(ctx, secret.ID, *secret.ActiveVersion)
	if err != nil {
		return "", apperr.Internal("load secret version failed", err)
	}
	plain, err := envelope.Open(s.kek, envelope.Blob{
		Ciphertext: version.Ciphertext, Nonce: version.Nonce, WrappedDEK: version.WrappedDEK,
		KEKKeyID: version.KEKKeyID, Algorithm: version.Algorithm, ValueHash: version.ValueHash,
	}, envelope.AAD(secret.Env, secret.Namespace, secret.Name, version.Version))
	if err != nil {
		return "", apperr.Unavailable("secret decryption unavailable")
	}
	return string(plain), nil
}

// SecretExists 报告命名密钥是否存在且已激活（不解密）。
func (s *SecretService) SecretExists(ctx context.Context, env, namespace, name string) (bool, error) {
	if s == nil || s.secrets == nil {
		return false, nil
	}
	secret, err := s.secrets.GetByName(ctx, env, namespace, name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		return false, apperr.Internal("lookup secret failed", err)
	}
	return secret.Status == domain.SecretStatusActive && secret.ActiveVersion != nil, nil
}

// ApplyInstallPlaintexts 把安装向导提交的敏感覆盖写入密钥子域（创建或轮换并自动审批）。
func (s *SecretService) ApplyInstallPlaintexts(ctx context.Context, env string,
	overrides map[string]string, registry interface {
		Lookup(namespace, key string) (domain.KeySpec, bool)
		Namespaces() []string
	}, actor Actor) error {
	if s == nil || len(overrides) == 0 || registry == nil {
		return nil
	}
	reviewer := Actor{
		SubjectID: "system:config-bootstrap-reviewer",
		ActorType: "system",
		Channel:   actor.Channel,
		TraceID:   actor.TraceID,
	}
	for key, plaintext := range overrides {
		plaintext = strings.TrimSpace(plaintext)
		if plaintext == "" {
			continue
		}
		namespace, keySpec, ok := lookupSensitive(registry, key)
		if !ok {
			continue
		}
		secret, err := s.secrets.GetByName(ctx, env, namespace, keySpec.Key)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return apperr.Internal("lookup secret failed", err)
		}
		if errors.Is(err, domain.ErrNotFound) {
			created, cerr := s.CreateSecret(ctx, CreateSecretInput{
				Env: env, Namespace: namespace, Name: keySpec.Key,
				Description: keySpec.Description, Owner: keySpec.Owner,
				Plaintext: plaintext, Actor: actor,
			})
			if cerr != nil {
				return cerr
			}
			if _, serr := s.SubmitSecret(ctx, created.ID, actor); serr != nil {
				return serr
			}
			created, aerr := s.ApproveSecret(ctx, ApproveSecretInput{
				ID: created.ID, Decision: domain.DecisionApproved,
				Comment: "install wizard override", Actor: reviewer,
			})
			if aerr != nil {
				return aerr
			}
			if gerr := s.ensureBootstrapGrant(ctx, created, reviewer); gerr != nil {
				return gerr
			}
			continue
		}
		if secret.Status != domain.SecretStatusActive {
			continue
		}
		rotated, rerr := s.RotateSecret(ctx, RotateSecretInput{
			ID: secret.ID, Plaintext: plaintext, Comment: "install wizard override", Actor: actor,
		})
		if rerr != nil {
			return rerr
		}
		if _, aerr := s.ApproveSecret(ctx, ApproveSecretInput{
			ID: rotated.ID, Decision: domain.DecisionApproved,
			Comment: "install wizard override", Actor: reviewer,
		}); aerr != nil {
			return aerr
		}
	}
	return nil
}

func lookupSensitive(registry interface {
	Lookup(namespace, key string) (domain.KeySpec, bool)
	Namespaces() []string
}, key string) (string, domain.KeySpec, bool) {
	for _, namespace := range registry.Namespaces() {
		keySpec, ok := registry.Lookup(namespace, key)
		if !ok {
			continue
		}
		if keySpec.Sensitive {
			return namespace, keySpec, true
		}
	}
	return "", domain.KeySpec{}, false
}
