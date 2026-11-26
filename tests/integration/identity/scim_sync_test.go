// SCIM 批量同步的幂等与规模冒烟（AC-P16-SCIM-2）。
//
// 用内存仓储跑完整用例链路（不经 HTTP），验证「同一批次重复执行不产生重复账号或绑定」。
// 真实 10k 用户 / 50 组的时延验收见 docs/identity-authn.md §21.1.9（需 staging + MySQL）。
package identity_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
)

// syncIDs 是并发安全的自增主键生成器。
type syncIDs struct {
	mu sync.Mutex
	n  int64
}

func (s *syncIDs) NextString() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return fmt.Sprintf("%d", s.n), nil
}

// syncProvisioner 记录投影调用次数，模拟 IAM 绑定侧。
type syncProvisioner struct {
	mu      sync.Mutex
	granted map[string]int
	revoked map[string]int
}

func newSyncProvisioner() *syncProvisioner {
	return &syncProvisioner{granted: map[string]int{}, revoked: map[string]int{}}
}

func (p *syncProvisioner) GrantGroupRoles(_ context.Context, accountID, _ string, codes []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.granted[accountID] += len(codes)
	return nil
}

func (p *syncProvisioner) RevokeGroupRoles(_ context.Context, accountID, _, _ string, codes []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.revoked[accountID] += len(codes)
	return nil
}

func (p *syncProvisioner) ValidateRoleCodes(_ context.Context, _ []string) error { return nil }

// TestScimBatchSyncIsIdempotent 验证同一批次重复执行不产生重复账号 / 组 / 成员。
func TestScimBatchSyncIsIdempotent(t *testing.T) {
	const (
		userCount  = 200
		groupCount = 20
		perGroup   = 10
	)
	ctx := context.Background()
	accounts := persistence.NewMemoryAccountRepository()
	tokens, groups := persistence.NewMemoryScimRepositories()
	provisioner := newSyncProvisioner()
	svc, err := app.NewScimService(app.ScimServiceConfig{
		Accounts:      accounts,
		Identities:    persistence.NewMemoryOAuthIdentityRepository(),
		Groups:        groups,
		Tokens:        tokens,
		Sessions:      persistence.NewMemorySessionRepository(),
		IDs:           &syncIDs{},
		Roles:         provisioner,
		Clock:         fixedClock{now: time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)},
		Provider:      "okta",
		AutoProvision: true,
		GroupRoleMap:  map[string]string{"team-0": "ops_admin"},
		PageSize:      50,
		TokenGrace:    168 * time.Hour,
	})
	if err != nil {
		t.Fatalf("new scim service: %v", err)
	}

	syncOnce := func() (createdUsers, duplicateUsers int) {
		for i := 0; i < userCount; i++ {
			_, createErr := svc.CreateUser(ctx, app.ScimUserInput{
				ExternalID: fmt.Sprintf("ext-%03d", i),
				UserName:   fmt.Sprintf("user-%03d@corp.example", i),
			}, "trace-user")
			switch {
			case createErr == nil:
				createdUsers++
			case isUniqueness(createErr):
				duplicateUsers++
			default:
				t.Fatalf("create user %d: %v", i, createErr)
			}
		}
		for g := 0; g < groupCount; g++ {
			members := make([]string, 0, perGroup)
			for j := 0; j < perGroup; j++ {
				members = append(members, fmt.Sprintf("ext-%03d", (g*perGroup+j)%userCount))
			}
			_, groupErr := svc.CreateGroup(ctx, app.ScimGroupInput{
				ExternalID:  fmt.Sprintf("grp-%02d", g),
				DisplayName: fmt.Sprintf("team-%d", g),
				Members:     members,
			}, "trace-group")
			if groupErr != nil && !isUniqueness(groupErr) {
				t.Fatalf("create group %d: %v", g, groupErr)
			}
		}
		return createdUsers, duplicateUsers
	}

	created, duplicates := syncOnce()
	if created != userCount || duplicates != 0 {
		t.Fatalf("first round created=%d duplicates=%d, want %d/0", created, duplicates, userCount)
	}
	created, duplicates = syncOnce()
	if created != 0 || duplicates != userCount {
		t.Fatalf("second round created=%d duplicates=%d, want 0/%d", created, duplicates, userCount)
	}

	listed, total, err := accounts.List(ctx, domain.AccountListFilter{
		Channel: domain.ChannelAdmin, ExcludeDeleted: true, Limit: 1000,
	})
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if total != userCount || len(listed) != userCount {
		t.Fatalf("accounts total=%d len=%d, want %d", total, len(listed), userCount)
	}
	groupPage, groupTotal, err := groups.List(ctx, domain.ScimGroupFilter{
		Provider: "okta", Limit: 100,
	})
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	if groupTotal != groupCount || len(groupPage) != groupCount {
		t.Fatalf("groups total=%d len=%d, want %d", groupTotal, len(groupPage), groupCount)
	}
	memberCount := 0
	for _, group := range groupPage {
		members, listErr := groups.ListMembers(ctx, group.ID)
		if listErr != nil {
			t.Fatalf("list members: %v", listErr)
		}
		memberCount += len(members)
	}
	if memberCount != groupCount*perGroup {
		t.Fatalf("members = %d, want %d", memberCount, groupCount*perGroup)
	}

	// 只有 team-0 映射到角色：其余组不产生授权。
	provisioner.mu.Lock()
	defer provisioner.mu.Unlock()
	if len(provisioner.granted) != perGroup {
		t.Fatalf("granted accounts = %d, want %d", len(provisioner.granted), perGroup)
	}
}

// isUniqueness 报告错误是否为 SCIM 唯一性冲突。
func isUniqueness(err error) bool {
	var scimErr *app.ScimError
	return errors.As(err, &scimErr) && scimErr.ScimType == "uniqueness"
}

// fixedClock 是固定测试时钟。
type fixedClock struct{ now time.Time }

// Now 返回固定时间。
func (c fixedClock) Now() time.Time { return c.now }
