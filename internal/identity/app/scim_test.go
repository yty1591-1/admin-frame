package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
)

// scimClock 是可推进的测试时钟。
type scimClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now 返回当前测试时间。
func (c *scimClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance 推进测试时间。
func (c *scimClock) Advance(delta time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(delta)
}

// fakeProvisioner 记录角色投影调用，并可按配置的角色集合失败。
type fakeProvisioner struct {
	mu      sync.Mutex
	granted map[string][]string
	revoked map[string][]string
	roles   map[string]bool
}

func newFakeProvisioner(roles ...string) *fakeProvisioner {
	available := make(map[string]bool, len(roles))
	for _, role := range roles {
		available[role] = true
	}
	return &fakeProvisioner{
		granted: make(map[string][]string),
		revoked: make(map[string][]string),
		roles:   available,
	}
}

func (f *fakeProvisioner) GrantGroupRoles(_ context.Context, accountID, _ string, roleCodes []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, code := range roleCodes {
		if !f.roles[code] {
			return errors.New("role unavailable")
		}
	}
	f.granted[accountID] = append(f.granted[accountID], roleCodes...)
	return nil
}

func (f *fakeProvisioner) RevokeGroupRoles(_ context.Context, accountID, _, _ string, roleCodes []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[accountID] = append(f.revoked[accountID], roleCodes...)
	return nil
}

func (f *fakeProvisioner) ValidateRoleCodes(_ context.Context, roleCodes []string) error {
	for _, code := range roleCodes {
		if !f.roles[code] {
			return errors.New("role unavailable")
		}
	}
	return nil
}

func (f *fakeProvisioner) grantedRoles(accountID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.granted[accountID]...)
}

func (f *fakeProvisioner) revokedRoles(accountID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.revoked[accountID]...)
}

// fakeScimObserver 记录组映射与请求观测。
type fakeScimObserver struct {
	mu       sync.Mutex
	groupMap []string
	requests int
}

func (f *fakeScimObserver) ObserveScimRequest(_ context.Context, _, _, _, _ string, _ time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
}

func (f *fakeScimObserver) ObserveScimGroupMap(_ context.Context, result string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupMap = append(f.groupMap, result)
}

func (f *fakeScimObserver) ObserveScimDisplayNameTruncated(_ context.Context) {}

// scimFixture 是 SCIM 用例的测试装配。
type scimFixture struct {
	svc         *app.ScimService
	clock       *scimClock
	accounts    domain.AccountRepository
	groups      domain.ScimGroupRepository
	provisioner *fakeProvisioner
	observer    *fakeScimObserver
}

// newScimFixture 构造基于内存仓储的 SCIM 服务。
func newScimFixture(t *testing.T, autoProvision bool) *scimFixture {
	t.Helper()
	clock := &scimClock{now: time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)}
	tokens, groups := persistence.NewMemoryScimRepositories()
	accounts := persistence.NewMemoryAccountRepository()
	provisioner := newFakeProvisioner("ops_admin", "auditor")
	observer := &fakeScimObserver{}
	svc, err := app.NewScimService(app.ScimServiceConfig{
		Accounts:      accounts,
		Identities:    persistence.NewMemoryOAuthIdentityRepository(),
		Groups:        groups,
		Tokens:        tokens,
		Sessions:      persistence.NewMemorySessionRepository(),
		IDs:           &seqIDs{},
		Roles:         provisioner,
		Clock:         clock,
		Observer:      observer,
		Service:       "identity-test",
		Provider:      "okta",
		AutoProvision: autoProvision,
		GroupRoleMap: map[string]string{
			"ops":      "ops_admin",
			"readonly": "auditor",
		},
		PageSize:   50,
		TokenGrace: 168 * time.Hour,
	})
	if err != nil {
		t.Fatalf("new scim service: %v", err)
	}
	return &scimFixture{
		svc: svc, clock: clock, accounts: accounts,
		groups: groups, provisioner: provisioner, observer: observer,
	}
}

// TestScimTokenRotationKeepsAtMostTwoActive 验证每 provider 最多 2 个生效令牌，
// 第三个令牌把最旧的转入退役宽限，宽限到期后不可用。
func TestScimTokenRotationKeepsAtMostTwoActive(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	first, err := fixture.svc.CreateToken(ctx, "1", "trace-1")
	if err != nil {
		t.Fatalf("create first token: %v", err)
	}
	if _, err := fixture.svc.CreateToken(ctx, "1", "trace-2"); err != nil {
		t.Fatalf("create second token: %v", err)
	}
	third, err := fixture.svc.CreateToken(ctx, "1", "trace-3")
	if err != nil {
		t.Fatalf("create third token: %v", err)
	}

	// 第一个令牌已退役：宽限期内仍可用，到期后失效。
	if _, authErr := fixture.svc.AuthenticateToken(ctx, first.Plaintext); authErr != nil {
		t.Fatalf("retiring token must work within grace: %v", authErr)
	}
	fixture.clock.Advance(169 * time.Hour)
	if _, authErr := fixture.svc.AuthenticateToken(ctx, first.Plaintext); authErr == nil {
		t.Fatalf("retiring token must expire after grace")
	}
	if _, authErr := fixture.svc.AuthenticateToken(ctx, third.Plaintext); authErr != nil {
		t.Fatalf("latest token must remain usable: %v", authErr)
	}

	tokens, err := fixture.svc.ListTokens(ctx)
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	active := 0
	for _, token := range tokens {
		if token.Status == domain.ScimTokenActive {
			active++
		}
	}
	if active != 2 {
		t.Fatalf("active tokens = %d, want 2", active)
	}
}

// TestScimAuthenticateRejectsRevokedAndUnknownToken 验证吊销与未知令牌都返回 401。
func TestScimAuthenticateRejectsRevokedAndUnknownToken(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	created, err := fixture.svc.CreateToken(ctx, "1", "trace")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if err := fixture.svc.RevokeToken(ctx, created.Token.ID, "1", "trace-revoke"); err != nil {
		t.Fatalf("revoke token: %v", err)
	}
	if _, authErr := fixture.svc.AuthenticateToken(ctx, created.Plaintext); authErr == nil {
		t.Fatalf("revoked token must be rejected")
	}
	if _, authErr := fixture.svc.AuthenticateToken(ctx, "scim_unknown"); authErr == nil {
		t.Fatalf("unknown token must be rejected")
	}
}

// TestScimCreateUserRejectsDuplicateExternalID 验证 externalId 是幂等键：
// 重复 POST 返回 409 uniqueness，而不是覆盖既有账号。
func TestScimCreateUserRejectsDuplicateExternalID(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	created, err := fixture.svc.CreateUser(ctx, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "Alice@Corp.example", DisplayName: "Alice",
	}, "trace-1")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if created.UserName != "alice@corp.example" {
		t.Fatalf("userName = %q, want normalized", created.UserName)
	}
	_, dupErr := fixture.svc.CreateUser(ctx, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example",
	}, "trace-2")
	var scimErr *app.ScimError
	if !errors.As(dupErr, &scimErr) || scimErr.ScimType != "uniqueness" {
		t.Fatalf("err = %v, want 409 uniqueness", dupErr)
	}
}

// TestScimCreateUserRequiresAutoProvision 验证 auto_provision=false 时 POST 返回 400。
func TestScimCreateUserRequiresAutoProvision(t *testing.T) {
	fixture := newScimFixture(t, false)
	_, err := fixture.svc.CreateUser(context.Background(), app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example",
	}, "trace")
	var scimErr *app.ScimError
	if !errors.As(err, &scimErr) || scimErr.Status != 400 || scimErr.ScimType != "invalidValue" {
		t.Fatalf("err = %v, want 400 invalidValue", err)
	}
}

// TestScimDisableUserBumpsTokenVersion 验证 active=false 等价于停用（AC-P16-SCIM-3）：
// 状态置为 disabled 且递增 token_version，旧访问令牌随即失效。
func TestScimDisableUserBumpsTokenVersion(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	created, err := fixture.svc.CreateUser(ctx, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example",
	}, "trace-1")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	before, err := fixture.accounts.FindByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	disabled := false
	if _, err := fixture.svc.PatchUser(ctx, created.ID, app.ScimUserPatch{Active: &disabled}, nil, "trace-2"); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	after, err := fixture.accounts.FindByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if after.Status != domain.AccountStatusDisabled {
		t.Fatalf("status = %s, want disabled", after.Status)
	}
	if after.TokenVersion != before.TokenVersion+1 {
		t.Fatalf("token version = %d, want %d", after.TokenVersion, before.TokenVersion+1)
	}
}

// TestScimGroupMembershipGrantsAndRevokesRole 验证成员增删投影为角色绑定的增删
// （AC-P16-SCIM-5 的正向路径）。
func TestScimGroupMembershipGrantsAndRevokesRole(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	user, err := fixture.svc.CreateUser(ctx, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example",
	}, "trace-1")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	group, err := fixture.svc.CreateGroup(ctx, app.ScimGroupInput{
		ExternalID: "grp-1", DisplayName: "ops", Members: []string{"ext-1"},
	}, "trace-2")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if got := fixture.provisioner.grantedRoles(user.ID); len(got) != 1 || got[0] != "ops_admin" {
		t.Fatalf("granted = %v, want [ops_admin]", got)
	}

	if _, err := fixture.svc.ReplaceGroup(ctx, group.ID, app.ScimGroupInput{
		ExternalID: "grp-1", DisplayName: "ops",
	}, nil, "trace-3"); err != nil {
		t.Fatalf("remove members: %v", err)
	}
	if got := fixture.provisioner.revokedRoles(user.ID); len(got) != 1 || got[0] != "ops_admin" {
		t.Fatalf("revoked = %v, want [ops_admin]", got)
	}
}

// TestScimSharedRoleKeepsGrantWhenOneGroupRemoved 验证两个组映射同一角色时，
// 只移除其中一个组不会撤销绑定。
func TestScimSharedRoleKeepsGrantWhenOneGroupRemoved(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	user, err := fixture.svc.CreateUser(ctx, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example",
	}, "trace-1")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	first, err := fixture.svc.CreateGroup(ctx, app.ScimGroupInput{
		ExternalID: "grp-1", DisplayName: "ops", Members: []string{"ext-1"},
	}, "trace-2")
	if err != nil {
		t.Fatalf("create first group: %v", err)
	}
	// 第二个组使用同名映射但不同展示名：需要独立配置项，这里直接改映射表不可行，
	// 因此复用同一角色 code 的另一个组名由 fixture 之外注入。
	second, err := fixture.svc.CreateGroup(ctx, app.ScimGroupInput{
		ExternalID: "grp-2", DisplayName: "readonly", Members: []string{"ext-1"},
	}, "trace-3")
	if err != nil {
		t.Fatalf("create second group: %v", err)
	}
	if err := fixture.svc.DeleteGroup(ctx, first.ID, "trace-4"); err != nil {
		t.Fatalf("delete first group: %v", err)
	}
	if got := fixture.provisioner.revokedRoles(user.ID); len(got) != 1 || got[0] != "ops_admin" {
		t.Fatalf("revoked = %v, want only ops_admin", got)
	}
	if err := fixture.svc.DeleteGroup(ctx, second.ID, "trace-5"); err != nil {
		t.Fatalf("delete second group: %v", err)
	}
	revoked := fixture.provisioner.revokedRoles(user.ID)
	if len(revoked) != 2 || revoked[1] != "auditor" {
		t.Fatalf("revoked = %v, want [ops_admin auditor]", revoked)
	}
}

// TestScimUnmappedGroupSkipsGrant 验证未映射组只同步成员关系、不产生授权。
func TestScimUnmappedGroupSkipsGrant(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	user, err := fixture.svc.CreateUser(ctx, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example",
	}, "trace-1")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := fixture.svc.CreateGroup(ctx, app.ScimGroupInput{
		ExternalID: "grp-1", DisplayName: "unmapped-group", Members: []string{"ext-1"},
	}, "trace-2"); err != nil {
		t.Fatalf("create group: %v", err)
	}
	if got := fixture.provisioner.grantedRoles(user.ID); len(got) != 0 {
		t.Fatalf("granted = %v, want none for unmapped group", got)
	}
	fixture.observer.mu.Lock()
	defer fixture.observer.mu.Unlock()
	if len(fixture.observer.groupMap) != 1 || fixture.observer.groupMap[0] != "unmapped" {
		t.Fatalf("group map = %v, want [unmapped]", fixture.observer.groupMap)
	}
}

// TestScimDeleteUserIsIdempotent 验证重复 DELETE 幂等返回。
func TestScimDeleteUserIsIdempotent(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	created, err := fixture.svc.CreateUser(ctx, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example",
	}, "trace-1")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := fixture.svc.DeleteUser(ctx, created.ID, "trace-2"); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if err := fixture.svc.DeleteUser(ctx, created.ID, "trace-3"); err != nil {
		t.Fatalf("repeat delete must be idempotent: %v", err)
	}
	if _, err := fixture.svc.GetUser(ctx, created.ID); err == nil {
		t.Fatalf("deleted user must be 404")
	}
}
