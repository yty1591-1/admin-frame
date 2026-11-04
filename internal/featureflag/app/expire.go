package app

import (
	"context"
	"time"

	"github.com/admin-frame/admin-frame/internal/featureflag/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// ExpireService 是到期治理任务（feature-flag-plan.md §2.E）：
// 周期扫描 expires_at 已过且仍 enabled 的 flag，置 disabled 并写
// featureflag.expire 元审计（actor_type=system）——「无长期遗留 flag」
// 判据的执行器；permanent flag 豁免（仓储 ListExpired 已过滤）。
type ExpireService struct {
	repos domain.Repository
	audit auditContext
	now   func() time.Time
}

// NewExpireService 构造到期治理用例。
func NewExpireService(repos domain.Repository, emitter audit.Emitter, ids id.Generator,
	opts Options) (*ExpireService, error) {
	switch {
	case repos == nil:
		return nil, apperr.Internal("expire service requires a repository", nil)
	case emitter == nil:
		return nil, apperr.Internal("expire service requires an audit emitter", nil)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &ExpireService{
		repos: repos,
		audit: auditContext{
			emitter: emitter, ids: ids, service: opts.Service,
			now: now, observer: opts.Observer,
		},
		now: now,
	}, nil
}

// Run 执行一轮到期扫描；返回禁用数量。单批上限 200——一次扫描处理不完
// 下轮继续（任务幂等，重复禁用无副作用）。
func (s *ExpireService) Run(ctx context.Context) (int, error) {
	expired, err := s.repos.ListExpired(ctx, s.now(), 200)
	if err != nil {
		return 0, apperr.Internal("list expired flags failed", err)
	}
	count := 0
	system := Actor{SubjectID: "system", ActorType: "system", Channel: domain.ChannelAdmin}
	for _, flag := range expired {
		next, err := flag.Transition(domain.StatusDisabled, s.now())
		if err != nil {
			// 并发已禁用：跳过（任务幂等）。
			continue
		}
		next.Version = flag.Version + 1
		if err := s.repos.Update(ctx, next, flag.Version); err != nil {
			if err == domain.ErrVersionConflict {
				continue
			}
			return count, apperr.Internal("expire flag failed", err)
		}
		attrs := map[string]string{"expires_at": next.ExpiresAt.UTC().Format(time.RFC3339)}
		if err := s.audit.emit(ctx, system, ActionFlagExpire, next.Key,
			audit.OutcomeSuccess, "flag expired and disabled automatically", attrs); err != nil {
			return count, apperr.Internal("emit expire audit failed", err)
		}
		count++
	}
	return count, nil
}
