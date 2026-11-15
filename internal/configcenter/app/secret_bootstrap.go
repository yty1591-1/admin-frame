package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// EnsureAutoSecrets 对声明了 secret_rule.auto_generate 的敏感键幂等生成并入库。
//
// 已存在同名密钥时跳过；无规则且 Required 的敏感键返回错误（fail-fast）。
func (s *SecretService) EnsureAutoSecrets(ctx context.Context, env string, registry *spec.Registry, actor Actor) error {
	if registry == nil {
		return apperr.Internal("secret bootstrap requires registry", nil)
	}
	if err := requireActor(actor); err != nil {
		return err
	}
	for _, namespace := range registry.Namespaces() {
		for _, keySpec := range registry.List(namespace) {
			if !keySpec.Sensitive {
				continue
			}
			if keySpec.SecretRule == nil || !keySpec.SecretRule.AutoGenerate {
				if keySpec.Required {
					_, err := s.secrets.GetByName(ctx, env, namespace, keySpec.Key)
					if errors.Is(err, domain.ErrNotFound) {
						return apperr.BadRequest(fmt.Sprintf(
							"required secret %s.%s missing and cannot auto-generate", namespace, keySpec.Key))
					}
				}
				continue
			}
			if _, err := s.secrets.GetByName(ctx, env, namespace, keySpec.Key); err == nil {
				continue
			} else if !errors.Is(err, domain.ErrNotFound) {
				return apperr.Internal("lookup secret failed", err)
			}
			plaintext, err := keySpec.SecretRule.Generate()
			if err != nil {
				return apperr.Internal("generate secret failed", err)
			}
			secret, err := s.CreateSecret(ctx, CreateSecretInput{
				Env: env, Namespace: namespace, Name: keySpec.Key,
				Description: keySpec.Description, Owner: keySpec.Owner,
				Plaintext: plaintext, Actor: actor,
			})
			if err != nil {
				return err
			}
			// 自动生成密钥直接送审并需人工审批；开发环境可再 Approve。
			if _, err := s.SubmitSecret(ctx, secret.ID, actor); err != nil {
				return err
			}
		}
	}
	return nil
}
