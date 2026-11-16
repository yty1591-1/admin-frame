package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

const bootstrapReviewer = "system:config-bootstrap-reviewer"

// EnsureAutoSecrets 对声明了 secret_rule.auto_generate 的敏感键幂等生成并入库。
//
// 已存在同名密钥时跳过；无规则且 Required 的敏感键返回错误（fail-fast）。
// 自动生成链路使用双系统主体完成双人审批，使空环境可一键启动（D16 / AC-CC-17）。
func (s *SecretService) EnsureAutoSecrets(ctx context.Context, env string, registry *spec.Registry, actor Actor) error {
	if registry == nil {
		return apperr.Internal("secret bootstrap requires registry", nil)
	}
	if err := requireActor(actor); err != nil {
		return err
	}
	reviewer := Actor{
		SubjectID: bootstrapReviewer,
		ActorType: "system",
		Channel:   actor.Channel,
		TraceID:   actor.TraceID,
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
			secret, err := s.secrets.GetByName(ctx, env, namespace, keySpec.Key)
			if err == nil {
				if err := s.ensureBootstrapGrant(ctx, secret, reviewer); err != nil {
					return err
				}
				continue
			} else if !errors.Is(err, domain.ErrNotFound) {
				return apperr.Internal("lookup secret failed", err)
			}
			plaintext, err := keySpec.SecretRule.Generate()
			if err != nil {
				return apperr.Internal("generate secret failed", err)
			}
			secret, err = s.CreateSecret(ctx, CreateSecretInput{
				Env: env, Namespace: namespace, Name: keySpec.Key,
				Description: keySpec.Description, Owner: keySpec.Owner,
				Plaintext: plaintext, Actor: actor,
			})
			if err != nil {
				return err
			}
			if _, err := s.SubmitSecret(ctx, secret.ID, actor); err != nil {
				return err
			}
			secret, err = s.ApproveSecret(ctx, ApproveSecretInput{
				ID: secret.ID, Decision: domain.DecisionApproved,
				Comment: "bootstrap auto-approve", Actor: reviewer,
			})
			if err != nil {
				return err
			}
			if err := s.ensureBootstrapGrant(ctx, secret, reviewer); err != nil {
				return err
			}
		}
	}
	return nil
}

// ensureBootstrapGrant 为启动期服务身份补齐可解析授权（幂等）。
//
// 默认覆盖 admin/biz/c/worker 短名与 spiffe://local/*；可用
// CONFIG_BOOTSTRAP_GRANT_IDENTITY 覆盖（逗号分隔）。JWT 等密钥须被
// 各业务进程 CONFIG_SERVICE_IDENTITY 命中，否则 resolve 返回 forbidden。
func (s *SecretService) ensureBootstrapGrant(ctx context.Context, secret domain.Secret, reviewer Actor) error {
	if secret.Status != domain.SecretStatusActive {
		return nil
	}
	submitter := Actor{
		SubjectID: "system:config-bootstrap",
		ActorType: "system",
		Channel:   reviewer.Channel,
		TraceID:   reviewer.TraceID,
	}
	for _, identity := range bootstrapGrantIdentities() {
		if _, err := s.grants.FindActive(ctx, secret.Env, secret.Namespace, identity, secret.ID, s.now().UTC()); err == nil {
			continue
		}
		current, err := s.secrets.GetByID(ctx, secret.ID)
		if err != nil {
			return apperr.Internal("reload secret for bootstrap grant failed", err)
		}
		if current.PendingAction != "" {
			return apperr.Conflict(fmt.Sprintf(
				"secret %s.%s has pending action %q; cannot bootstrap grant for %s",
				current.Namespace, current.Name, current.PendingAction, identity))
		}
		grant, err := s.CreateGrant(ctx, CreateGrantInput{
			SecretID: secret.ID, ServiceIdentity: identity, Actor: submitter,
		})
		if err != nil {
			return err
		}
		if _, err := s.ApproveSecret(ctx, ApproveSecretInput{
			ID: secret.ID, Decision: domain.DecisionApproved,
			Comment: "bootstrap grant " + grant.ServiceIdentity, Actor: reviewer,
		}); err != nil {
			return err
		}
	}
	return nil
}

// bootstrapGrantIdentities 返回启动期应获得 resolve 授权的服务身份列表。
func bootstrapGrantIdentities() []string {
	raw := strings.TrimSpace(os.Getenv("CONFIG_BOOTSTRAP_GRANT_IDENTITY"))
	if raw != "" {
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		seen := make(map[string]struct{}, len(parts))
		for _, part := range parts {
			id := strings.TrimSpace(part)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{
		"admin-api", "biz-api", "c-api", "worker",
		"spiffe://local/admin-api", "spiffe://local/biz-api",
		"spiffe://local/c-api", "spiffe://local/worker",
	}
}
