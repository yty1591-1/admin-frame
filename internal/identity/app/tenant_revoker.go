package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// revokePageSize 商户账号枚举分页大小。
//
// 停用一个大租户可能有上千账号；单页过大拉爆内存，过小放大往返次数。
// 与账号列表接口的默认上限保持一个量级即可。
const revokePageSize = 200

// TenantSessionRevoker 撤销一个租户（B 端商户）名下全部会话与令牌。
//
// ENT-09 D-02=①：租户停用 / 终止时立即撤销在册会话——停用后的租户不允许
// 「令牌还能换新」。由 identity 实现、tenant 通过端口调用，两个域不直接依赖。
//
// 一致性边界：撤销按账号逐个提交（identity 事务不与租户域共享），
// 中途失败会留下「部分账号已撤销」的状态。方向是 fail-closed——
// 已撤销的账号最多被要求重新登录，不会出现「该撤没撤」。
type TenantSessionRevoker struct {
	accounts domain.AccountRepository
	sessions domain.SessionRepository
	clock    Clock
}

// NewTenantSessionRevoker 构造租户会话撤销器；依赖必填。
func NewTenantSessionRevoker(accounts domain.AccountRepository, sessions domain.SessionRepository, clock Clock) (*TenantSessionRevoker, error) {
	if accounts == nil {
		return nil, apperr.Internal("tenant session revoker requires an account repository", nil)
	}
	if sessions == nil {
		return nil, apperr.Internal("tenant session revoker requires a session repository", nil)
	}
	if clock == nil {
		clock = realClock{}
	}
	return &TenantSessionRevoker{accounts: accounts, sessions: sessions, clock: clock}, nil
}

// RevokeByTenant 撤销商户下全部 B 端账号的会话，并递增活跃账号的 token_version
// 使已签发的访问令牌立即失效。返回撤销的会话数。
//
// 已停用账号不递增版本（BumpTokenVersion 对非活跃账号会被领域规则拒绝），
// 但仍执行会话撤销：停用账号的会话本应已被撤销，重复撤销是幂等的安全操作。
func (r *TenantSessionRevoker) RevokeByTenant(ctx context.Context, merchantID string) (int, error) {
	trimmed := strings.TrimSpace(merchantID)
	if trimmed == "" {
		return 0, apperr.BadRequest("merchant id is required")
	}

	revoked := 0
	offset := 0
	for {
		accounts, total, err := r.accounts.List(ctx, domain.AccountListFilter{
			Channel:    domain.ChannelBiz,
			MerchantID: trimmed,
			Limit:      revokePageSize,
			Offset:     offset,
		})
		if err != nil {
			return revoked, apperr.Internal("tenant session revoker: list accounts failed", err)
		}
		if len(accounts) == 0 {
			break
		}
		for _, account := range accounts {
			count, accountErr := r.revokeAccount(ctx, account)
			if accountErr != nil {
				return revoked, accountErr
			}
			revoked += count
		}
		offset += len(accounts)
		if total >= 0 && offset >= total {
			break
		}
	}
	return revoked, nil
}

// revokeAccount 撤销单个账号的会话；活跃账号同时递增 token_version。
func (r *TenantSessionRevoker) revokeAccount(ctx context.Context, account *domain.Account) (int, error) {
	now := r.clock.Now().UTC()
	if account.Status == domain.AccountStatusActive {
		expected := account.Version
		if err := account.BumpTokenVersion(now); err != nil {
			return 0, apperr.Internal("tenant session revoker: bump token version failed", err)
		}
		if err := r.accounts.Save(ctx, account, expected); err != nil {
			return 0, apperr.Internal("tenant session revoker: save account failed", err)
		}
	}
	count, err := r.sessions.RevokeByAccount(ctx, account.ID, domain.RevokeReasonTenantDisabled, now)
	if err != nil {
		return 0, fmt.Errorf("tenant session revoker: revoke sessions of %s: %w", account.ID, err)
	}
	return count, nil
}
