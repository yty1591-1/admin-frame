package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// scimTestNow 是新建 SCIM 服务使用的固定测试时间。
var scimTestNow = time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)

// boolPtr / int64Ptr 返回指针入参，便于构造 SCIM 增量字段。
func boolPtr(value bool) *bool { return &value }

func int64Ptr(value int64) *int64 { return &value }

// recordingAudit 记录 SCIM 用例发出的审计事件。
type recordingAudit struct {
	events []audit.Event
}

// Emit 实现 audit.Emitter。
func (r *recordingAudit) Emit(_ context.Context, event audit.Event) error {
	r.events = append(r.events, event)
	return nil
}

// recorded 返回已记录事件的副本。
func (r *recordingAudit) recorded() []audit.Event {
	return append([]audit.Event(nil), r.events...)
}

// failingAccounts 包装账号仓储，按需注入单方法故障（验证失败关闭语义）。
type failingAccounts struct {
	domain.AccountRepository
	findErr        error
	findSubjectErr error
	listErr        error
}

// FindByID 注入故障时返回错误，否则委托给内存仓储。
func (f *failingAccounts) FindByID(ctx context.Context, id string) (*domain.Account, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.AccountRepository.FindByID(ctx, id)
}

// FindBySubject 注入故障时返回错误，否则委托给内存仓储。
func (f *failingAccounts) FindBySubject(
	ctx context.Context,
	channel domain.Channel,
	merchantID, subject string,
) (*domain.Account, error) {
	if f.findSubjectErr != nil {
		return nil, f.findSubjectErr
	}
	return f.AccountRepository.FindBySubject(ctx, channel, merchantID, subject)
}

// List 注入故障时返回错误，否则委托给内存仓储。
func (f *failingAccounts) List(
	ctx context.Context,
	filter domain.AccountListFilter,
) ([]*domain.Account, int, error) {
	if f.listErr != nil {
		return nil, 0, f.listErr
	}
	return f.AccountRepository.List(ctx, filter)
}

// failingIdentities 包装外部身份仓储，按需注入故障。
type failingIdentities struct {
	domain.OAuthIdentityRepository
	findErr error
	listErr error
}

// FindByProviderSubject 注入故障时返回错误，否则委托给内存仓储。
func (f *failingIdentities) FindByProviderSubject(ctx context.Context, provider, subject string) (*domain.OAuthIdentity, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.OAuthIdentityRepository.FindByProviderSubject(ctx, provider, subject)
}

// ListByAccount 注入故障时返回错误，否则委托给内存仓储。
func (f *failingIdentities) ListByAccount(ctx context.Context, accountID string) ([]*domain.OAuthIdentity, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.OAuthIdentityRepository.ListByAccount(ctx, accountID)
}

// newScimTestService 构造 SCIM 服务；mutate 可替换仓储、审计等依赖。
func newScimTestService(t *testing.T, mutate func(*app.ScimServiceConfig)) *app.ScimService {
	t.Helper()
	tokens, groups := persistence.NewMemoryScimRepositories()
	cfg := app.ScimServiceConfig{
		Accounts:      persistence.NewMemoryAccountRepository(),
		Identities:    persistence.NewMemoryOAuthIdentityRepository(),
		Groups:        groups,
		Tokens:        tokens,
		Sessions:      persistence.NewMemorySessionRepository(),
		IDs:           &seqIDs{},
		Roles:         newFakeProvisioner("ops_admin", "auditor"),
		Clock:         &scimClock{now: scimTestNow},
		Service:       "identity-test",
		Provider:      "okta",
		AutoProvision: true,
		GroupRoleMap:  map[string]string{"ops": "ops_admin", "readonly": "auditor"},
		PageSize:      50,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	svc, err := app.NewScimService(cfg)
	if err != nil {
		t.Fatalf("new scim service: %v", err)
	}
	return svc
}

// mustCreateScimUser 创建 SCIM User，失败即终止用例。
func mustCreateScimUser(t *testing.T, svc *app.ScimService, in app.ScimUserInput) *app.ScimUser {
	t.Helper()
	user, err := svc.CreateUser(context.Background(), in, "trace-create")
	if err != nil {
		t.Fatalf("create scim user: %v", err)
	}
	return user
}

// seedChannelAccount 直接写入指定端别的活跃账号，用于验证通道边界。
func seedChannelAccount(t *testing.T, accounts domain.AccountRepository, channel domain.Channel) *domain.Account {
	t.Helper()
	subject := "seed-" + string(channel) + "@corp.example"
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID:      "seed-" + string(channel),
		Channel: channel,
		Subject: subject,
		Now:     scimTestNow,
	})
	if err != nil {
		t.Fatalf("new %s account: %v", channel, err)
	}
	if err := account.Activate(scimTestNow); err != nil {
		t.Fatalf("activate %s account: %v", channel, err)
	}
	if err := accounts.Save(context.Background(), account, 0); err != nil {
		t.Fatalf("save %s account: %v", channel, err)
	}
	return account
}

// requireScimError 断言错误是 SCIM 协议错误且状态码匹配。
func requireScimError(t *testing.T, err error, status int) {
	t.Helper()
	_ = scimErrorOf(t, err, status)
}

// scimErrorOf 断言并返回 SCIM 错误，供进一步校验 scimType 的用例使用。
func scimErrorOf(t *testing.T, err error, status int) *app.ScimError {
	t.Helper()
	var scimErr *app.ScimError
	if !errors.As(err, &scimErr) {
		t.Fatalf("error %v is not *app.ScimError", err)
	}
	if scimErr.Status != status {
		t.Fatalf("scim status = %d, want %d (%v)", scimErr.Status, status, err)
	}
	return scimErr
}

// TestScimGetUserReturnsView 验证按主键返回 User 字段与组投影。
func TestScimGetUserReturnsView(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	created := mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "Alice@Corp.example", DisplayName: "Alice",
	})
	group := mustCreateScimGroup(t, fixture.svc, app.ScimGroupInput{
		ExternalID: "grp-1", DisplayName: "ops", Members: []string{"ext-1"},
	})

	view, err := fixture.svc.GetUser(ctx, created.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if view.ID != created.ID || view.UserName != "alice@corp.example" || view.DisplayName != "Alice" {
		t.Fatalf("unexpected view: %+v", view)
	}
	if !view.Active || view.ExternalID != "ext-1" {
		t.Fatalf("active / externalId mismatch: %+v", view)
	}
	if view.Version < 1 || view.CreatedAt.IsZero() || view.UpdatedAt.IsZero() {
		t.Fatalf("meta fields missing: %+v", view)
	}
	if len(view.Groups) != 1 || view.Groups[0].Value != group.ID || view.Groups[0].Display != "ops" {
		t.Fatalf("groups = %+v, want single ops group", view.Groups)
	}
}

// TestScimGetUserNotFound 验证空 ID、未知 ID 与非 A 端账号统一返回 404。
func TestScimGetUserNotFound(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	foreign := seedChannelAccount(t, fixture.accounts, domain.ChannelC)

	cases := []struct {
		name string
		id   string
	}{
		{name: "empty id", id: "  "},
		{name: "unknown id", id: "999"},
		{name: "non admin channel", id: foreign.ID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := fixture.svc.GetUser(ctx, tc.id); err == nil {
				t.Fatal("expected 404")
			} else {
				requireScimError(t, err, 404)
			}
		})
	}
}

// TestScimListUsersPagination 验证 startIndex / count 分页与边界收敛。
func TestScimListUsersPagination(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	for _, subject := range []string{"alice@corp.example", "bob@corp.example", "carol@corp.example"} {
		mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
			ExternalID: "ext-" + subject, UserName: subject,
		})
	}

	first, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{StartIndex: 1, Count: 2})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if first.Total != 3 || len(first.Users) != 2 || first.StartIndex != 1 || first.ItemsPerPage != 2 {
		t.Fatalf("first page = %+v, want total 3 / 2 items", first)
	}

	second, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{StartIndex: 3, Count: 2})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if second.Total != 3 || len(second.Users) != 1 || second.ItemsPerPage != 1 {
		t.Fatalf("second page = %+v, want total 3 / 1 item", second)
	}

	beyond, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{StartIndex: 10, Count: 2})
	if err != nil {
		t.Fatalf("list beyond page: %v", err)
	}
	if beyond.Total != 3 || len(beyond.Users) != 0 {
		t.Fatalf("beyond page = %+v, want no items but total 3", beyond)
	}

	// count=0 表示只要 totalResults；非法负值收敛到默认值。
	for _, query := range []app.ScimUserListQuery{
		{StartIndex: 0, Count: 0},
		{StartIndex: -5, Count: -1},
	} {
		page, listErr := fixture.svc.ListUsers(ctx, query)
		if listErr != nil {
			t.Fatalf("list count-only: %v", listErr)
		}
		if page.Total != 3 || len(page.Users) != 0 || page.StartIndex != 1 {
			t.Fatalf("count-only page = %+v, want total 3 and startIndex 1", page)
		}
	}

	// count 超过上限按 100 截断，不应报错。
	big, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{StartIndex: 1, Count: 1000})
	if err != nil {
		t.Fatalf("list huge count: %v", err)
	}
	if len(big.Users) != 3 || big.ItemsPerPage != 3 {
		t.Fatalf("huge count page = %+v, want all 3 users", big)
	}
}

// TestScimListUsersUserNameLookup 验证 userName eq 的单条查询与剩余过滤条件。
func TestScimListUsersUserNameLookup(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	alice := mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "Alice@Corp.example", DisplayName: "Alice",
	})
	bob := mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-2", UserName: "bob@corp.example", DisplayName: "Bob", Active: boolPtr(false),
	})

	hit, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{UserName: " ALICE@corp.example ", Count: 10})
	if err != nil {
		t.Fatalf("lookup by userName: %v", err)
	}
	if hit.Total != 1 || len(hit.Users) != 1 || hit.Users[0].ID != alice.ID {
		t.Fatalf("userName hit = %+v, want alice", hit)
	}

	miss, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{UserName: "ghost@corp.example"})
	if err != nil {
		t.Fatalf("lookup missing userName: %v", err)
	}
	if miss.Total != 0 || len(miss.Users) != 0 {
		t.Fatalf("userName miss = %+v, want empty", miss)
	}

	displayMismatch, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{
		UserName: "alice@corp.example", DisplayName: "Bob",
	})
	if err != nil {
		t.Fatalf("lookup with displayName mismatch: %v", err)
	}
	if displayMismatch.Total != 0 || len(displayMismatch.Users) != 0 {
		t.Fatalf("displayName mismatch = %+v, want empty", displayMismatch)
	}

	activeMismatch, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{
		UserName: "alice@corp.example", Active: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("lookup with active mismatch: %v", err)
	}
	if activeMismatch.Total != 0 {
		t.Fatalf("active mismatch total = %d, want 0", activeMismatch.Total)
	}

	activeMatch, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{
		UserName: "alice@corp.example", Active: boolPtr(true), Count: 10,
	})
	if err != nil {
		t.Fatalf("lookup with active match: %v", err)
	}
	if activeMatch.Total != 1 || len(activeMatch.Users) != 1 {
		t.Fatalf("active match = %+v, want alice", activeMatch)
	}

	disabledMatch, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{
		UserName: "bob@corp.example", Active: boolPtr(false), Count: 10,
	})
	if err != nil {
		t.Fatalf("lookup disabled user: %v", err)
	}
	if disabledMatch.Total != 1 || len(disabledMatch.Users) != 1 || disabledMatch.Users[0].ID != bob.ID {
		t.Fatalf("disabled match = %+v, want bob", disabledMatch)
	}
}

// TestScimListUsersIDAndExternalIDLookup 验证主键 / 外部 ID 单条查询与软删排除。
func TestScimListUsersIDAndExternalIDLookup(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	alice := mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example",
	})
	gone := mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-2", UserName: "gone@corp.example",
	})
	if err := fixture.svc.DeleteUser(ctx, gone.ID, "trace-delete"); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	byID, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{ID: " " + alice.ID + " ", Count: 10})
	if err != nil {
		t.Fatalf("lookup by id: %v", err)
	}
	if byID.Total != 1 || len(byID.Users) != 1 || byID.Users[0].ID != alice.ID {
		t.Fatalf("id hit = %+v, want alice", byID)
	}

	idMiss, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{ID: "999"})
	if err != nil {
		t.Fatalf("lookup missing id: %v", err)
	}
	if idMiss.Total != 0 || len(idMiss.Users) != 0 {
		t.Fatalf("id miss = %+v, want empty", idMiss)
	}

	idDeleted, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{ID: gone.ID})
	if err != nil {
		t.Fatalf("lookup deleted id: %v", err)
	}
	if idDeleted.Total != 0 || len(idDeleted.Users) != 0 {
		t.Fatalf("deleted id = %+v, want empty", idDeleted)
	}

	// 主键命中后仍需应用 userName 过滤，避免过滤器被静默忽略。
	idNameMismatch, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{
		ID: alice.ID, UserName: "bob@corp.example",
	})
	if err != nil {
		t.Fatalf("lookup id with userName mismatch: %v", err)
	}
	if idNameMismatch.Total != 0 || len(idNameMismatch.Users) != 0 {
		t.Fatalf("id + userName mismatch = %+v, want empty", idNameMismatch)
	}

	byExternal, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{ExternalID: "ext-1", Count: 10})
	if err != nil {
		t.Fatalf("lookup by externalId: %v", err)
	}
	if byExternal.Total != 1 || len(byExternal.Users) != 1 || byExternal.Users[0].ID != alice.ID {
		t.Fatalf("externalId hit = %+v, want alice", byExternal)
	}

	externalMiss, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{ExternalID: "ext-ghost"})
	if err != nil {
		t.Fatalf("lookup missing externalId: %v", err)
	}
	if externalMiss.Total != 0 || len(externalMiss.Users) != 0 {
		t.Fatalf("externalId miss = %+v, want empty", externalMiss)
	}

	externalDeleted, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{ExternalID: "ext-2"})
	if err != nil {
		t.Fatalf("lookup deleted externalId: %v", err)
	}
	if externalDeleted.Total != 0 || len(externalDeleted.Users) != 0 {
		t.Fatalf("deleted externalId = %+v, want empty", externalDeleted)
	}
}

// TestScimListUsersDisplayNameAndActiveFilters 验证 displayName / active 列表过滤与分页。
func TestScimListUsersDisplayNameAndActiveFilters(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example", DisplayName: "Alice A",
	})
	mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-2", UserName: "bob@corp.example", DisplayName: "Bob B",
	})
	mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-3", UserName: "carol@corp.example", Active: boolPtr(false),
	})

	byName, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{DisplayName: "Alice A", Count: 10})
	if err != nil {
		t.Fatalf("filter displayName: %v", err)
	}
	if byName.Total != 1 || len(byName.Users) != 1 || byName.Users[0].UserName != "alice@corp.example" {
		t.Fatalf("displayName filter = %+v, want alice", byName)
	}

	nameMiss, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{DisplayName: "Nobody"})
	if err != nil {
		t.Fatalf("filter missing displayName: %v", err)
	}
	if nameMiss.Total != 0 || len(nameMiss.Users) != 0 {
		t.Fatalf("displayName miss = %+v, want empty", nameMiss)
	}

	active, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{Active: boolPtr(true), Count: 10})
	if err != nil {
		t.Fatalf("filter active: %v", err)
	}
	if active.Total != 2 || len(active.Users) != 2 {
		t.Fatalf("active filter = %+v, want 2 users", active)
	}

	disabled, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{Active: boolPtr(false), Count: 10})
	if err != nil {
		t.Fatalf("filter disabled: %v", err)
	}
	if disabled.Total != 1 || len(disabled.Users) != 1 || disabled.Users[0].UserName != "carol@corp.example" {
		t.Fatalf("disabled filter = %+v, want carol", disabled)
	}

	combined, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{
		DisplayName: "Bob B", Active: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("filter combined: %v", err)
	}
	if combined.Total != 0 || len(combined.Users) != 0 {
		t.Fatalf("combined filter = %+v, want empty", combined)
	}

	paged, err := fixture.svc.ListUsers(ctx, app.ScimUserListQuery{
		Active: boolPtr(true), StartIndex: 1, Count: 1,
	})
	if err != nil {
		t.Fatalf("filter with paging: %v", err)
	}
	if paged.Total != 2 || len(paged.Users) != 1 || paged.ItemsPerPage != 1 {
		t.Fatalf("paged filter = %+v, want total 2 / 1 item", paged)
	}
}

// TestScimListUsersRepositoryFailures 验证仓储故障时错误不被静默吞掉。
func TestScimListUsersRepositoryFailures(t *testing.T) {
	ctx := context.Background()
	storeErr := errors.New("account store down")
	accounts := &failingAccounts{
		AccountRepository: persistence.NewMemoryAccountRepository(),
		listErr:           storeErr,
	}
	svc := newScimTestService(t, func(cfg *app.ScimServiceConfig) { cfg.Accounts = accounts })
	if _, err := svc.ListUsers(ctx, app.ScimUserListQuery{}); !errors.Is(err, storeErr) {
		t.Fatalf("list error = %v, want %v", err, storeErr)
	}

	// 主键 / userName 查询的仓储故障必须原样上抛，不能被误判为「未找到」。
	accounts.findErr = storeErr
	if _, err := svc.ListUsers(ctx, app.ScimUserListQuery{ID: "42"}); !errors.Is(err, storeErr) {
		t.Fatalf("id query error = %v, want %v", err, storeErr)
	}
	accounts.findSubjectErr = storeErr
	if _, err := svc.ListUsers(ctx, app.ScimUserListQuery{UserName: "alice@corp.example"}); !errors.Is(err, storeErr) {
		t.Fatalf("userName query error = %v, want %v", err, storeErr)
	}

	// 身份仓储不可用时按 503 失败关闭。
	badIdentities := &failingIdentities{
		OAuthIdentityRepository: persistence.NewMemoryOAuthIdentityRepository(),
		findErr:                 errors.New("identity store down"),
	}
	identitySvc := newScimTestService(t, func(cfg *app.ScimServiceConfig) { cfg.Identities = badIdentities })
	_, err := identitySvc.ListUsers(ctx, app.ScimUserListQuery{ExternalID: "ext-1"})
	requireAppCode(t, err, apperr.CodeServiceUnavailable)

	// 映射存在但账号加载失败时上抛原始错误。
	identities := persistence.NewMemoryOAuthIdentityRepository()
	identity, err := domain.NewOAuthIdentity(domain.NewOAuthIdentityParams{
		ID: "id-1", AccountID: "42", Provider: "scim:okta", ProviderSubject: "ext-1", Now: scimTestNow,
	})
	if err != nil {
		t.Fatalf("new oauth identity: %v", err)
	}
	if err := identities.Save(ctx, identity); err != nil {
		t.Fatalf("save oauth identity: %v", err)
	}
	brokenAccounts := &failingAccounts{
		AccountRepository: persistence.NewMemoryAccountRepository(),
		findErr:           storeErr,
	}
	loadSvc := newScimTestService(t, func(cfg *app.ScimServiceConfig) {
		cfg.Accounts = brokenAccounts
		cfg.Identities = identities
	})
	if _, err := loadSvc.ListUsers(ctx, app.ScimUserListQuery{ExternalID: "ext-1"}); !errors.Is(err, storeErr) {
		t.Fatalf("externalId account load error = %v, want %v", err, storeErr)
	}
}

// TestScimUserViewRepositoryFailures 验证视图组装依赖故障时 GetUser 失败关闭。
func TestScimUserViewRepositoryFailures(t *testing.T) {
	ctx := context.Background()
	accounts := persistence.NewMemoryAccountRepository()
	account := seedChannelAccountAs(t, accounts, domain.ChannelAdmin, "alice@corp.example")

	identitySvc := newScimTestService(t, func(cfg *app.ScimServiceConfig) {
		cfg.Accounts = accounts
		cfg.Identities = &failingIdentities{
			OAuthIdentityRepository: persistence.NewMemoryOAuthIdentityRepository(),
			listErr:                 errors.New("identity store down"),
		}
	})
	_, err := identitySvc.GetUser(ctx, account.ID)
	requireAppCode(t, err, apperr.CodeServiceUnavailable)

	groupErr := errors.New("group store down")
	_, realGroups := persistence.NewMemoryScimRepositories()
	groupSvc := newScimTestService(t, func(cfg *app.ScimServiceConfig) {
		cfg.Accounts = accounts
		cfg.Groups = &failingGroups{ScimGroupRepository: realGroups, listErr: groupErr}
	})
	if _, err := groupSvc.GetUser(ctx, account.ID); !errors.Is(err, groupErr) {
		t.Fatalf("group view error = %v, want %v", err, groupErr)
	}
}

// TestScimReplaceUserUpdatesFields 验证替换 displayName / active 与 If-Match 语义。
func TestScimReplaceUserUpdatesFields(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	created := mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "Alice@Corp.example", DisplayName: "Alice",
	})

	updated, err := fixture.svc.ReplaceUser(ctx, created.ID, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "Alice@Corp.example", DisplayName: "Alice Cooper", Active: boolPtr(false),
	}, int64Ptr(created.Version), "trace-replace")
	if err != nil {
		t.Fatalf("replace user: %v", err)
	}
	if updated.DisplayName != "Alice Cooper" || updated.Active {
		t.Fatalf("updated view = %+v, want renamed and disabled", updated)
	}
	if updated.Version <= created.Version {
		t.Fatalf("version = %d, want > %d", updated.Version, created.Version)
	}
	account, err := fixture.accounts.FindByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.Status != domain.AccountStatusDisabled {
		t.Fatalf("status = %s, want disabled", account.Status)
	}
	if account.TokenVersion != 2 {
		t.Fatalf("token version = %d, want 2", account.TokenVersion)
	}

	// 旧版本重放必须 412，且不得覆盖当前状态。
	if _, err := fixture.svc.ReplaceUser(ctx, created.ID, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example", DisplayName: "Stale",
	}, int64Ptr(created.Version), "trace-stale"); err == nil {
		t.Fatal("stale If-Match must fail")
	} else {
		requireScimError(t, err, 412)
	}
	after, err := fixture.svc.GetUser(ctx, created.ID)
	if err != nil {
		t.Fatalf("get user after stale replace: %v", err)
	}
	if after.DisplayName != "Alice Cooper" {
		t.Fatalf("displayName = %q, want unchanged", after.DisplayName)
	}

	// 恢复启用：active=true 走 Enable 分支。
	enabled, err := fixture.svc.ReplaceUser(ctx, created.ID, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example", DisplayName: "Alice",
		Active: boolPtr(true),
	}, nil, "trace-enable")
	if err != nil {
		t.Fatalf("enable user: %v", err)
	}
	if !enabled.Active {
		t.Fatal("user must be active after replacement")
	}
}

// TestScimReplaceUserValidation 验证替换入参校验与不存在 / 非 A 端账号分支。
func TestScimReplaceUserValidation(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	alice := mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example", DisplayName: "Alice",
	})
	mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-2", UserName: "bob@corp.example",
	})

	// externalId 必填；校验失败不得留下部分变更（displayName 未被清空）。
	if _, err := fixture.svc.ReplaceUser(ctx, alice.ID, app.ScimUserInput{
		ExternalID: "  ", UserName: "alice@corp.example",
	}, nil, "trace-invalid"); err == nil {
		t.Fatal("empty externalId must fail")
	} else {
		requireScimError(t, err, 400)
	}
	after, err := fixture.svc.GetUser(ctx, alice.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if after.DisplayName != "Alice" {
		t.Fatalf("displayName = %q, want unchanged on validation failure", after.DisplayName)
	}

	if _, err := fixture.svc.ReplaceUser(ctx, alice.ID, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "  ",
	}, nil, "trace-invalid"); err == nil {
		t.Fatal("empty userName must fail")
	} else {
		requireScimError(t, err, 400)
	}

	// userName 与其它账号冲突。
	scimErr := scimErrorOf(t, mustReplace(t, fixture.svc, alice.ID, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "bob@corp.example",
	}), 409)
	if scimErr.ScimType != "uniqueness" {
		t.Fatalf("scimType = %q, want uniqueness", scimErr.ScimType)
	}

	// 不存在 / 空 ID / 非 A 端账号统一 404。
	for _, id := range []string{"999", " ", seedChannelAccount(t, fixture.accounts, domain.ChannelC).ID} {
		requireScimError(t, mustReplace(t, fixture.svc, id, app.ScimUserInput{
			ExternalID: "ext-1", UserName: "alice@corp.example",
		}), 404)
	}
}

// mustReplace 调用 ReplaceUser 并返回错误，供表驱动断言使用。
func mustReplace(t *testing.T, svc *app.ScimService, id string, in app.ScimUserInput) error {
	t.Helper()
	_, err := svc.ReplaceUser(context.Background(), id, in, nil, "trace-replace")
	return err
}

// TestScimReplaceUserRebindsExternalID 验证外部 ID 重绑与冲突保护。
func TestScimReplaceUserRebindsExternalID(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	alice := mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example", DisplayName: "Alice",
	})

	rebound, err := fixture.svc.ReplaceUser(ctx, alice.ID, app.ScimUserInput{
		ExternalID: " ext-9 ", UserName: "alice@corp.example", DisplayName: "Alice",
	}, nil, "trace-rebind")
	if err != nil {
		t.Fatalf("rebind externalId: %v", err)
	}
	if rebound.ExternalID != "ext-9" {
		t.Fatalf("externalId = %q, want ext-9", rebound.ExternalID)
	}

	// 旧外部 ID 已释放，可绑定到新账号。
	if _, err := fixture.svc.CreateUser(ctx, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "dave@corp.example",
	}, "trace-create-dave"); err != nil {
		t.Fatalf("old externalId must be reusable: %v", err)
	}

	// 相同外部 ID 重复提交幂等成功（不触发重绑）。
	if _, err := fixture.svc.ReplaceUser(ctx, alice.ID, app.ScimUserInput{
		ExternalID: "ext-9", UserName: "alice@corp.example", DisplayName: "Alice",
	}, nil, "trace-idempotent"); err != nil {
		t.Fatalf("same externalId must be idempotent: %v", err)
	}

	// 绑到其它账号已占用的外部 ID 必须 409，且不覆盖既有绑定。
	mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-2", UserName: "bob@corp.example",
	})
	scimErr := scimErrorOf(t, mustReplace(t, fixture.svc, alice.ID, app.ScimUserInput{
		ExternalID: "ext-2", UserName: "alice@corp.example",
	}), 409)
	if scimErr.ScimType != "uniqueness" {
		t.Fatalf("scimType = %q, want uniqueness", scimErr.ScimType)
	}
	view, err := fixture.svc.GetUser(ctx, alice.ID)
	if err != nil {
		t.Fatalf("get user after conflict: %v", err)
	}
	if view.ExternalID != "ext-9" {
		t.Fatalf("externalId = %q, want unchanged ext-9", view.ExternalID)
	}

	requireScimError(t, mustReplace(t, fixture.svc, alice.ID, app.ScimUserInput{
		ExternalID: "   ", UserName: "alice@corp.example",
	}), 400)
}

// TestScimCreateUserValidation 验证创建入参校验、展示名截断与停用。
func TestScimCreateUserValidation(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()

	cases := []struct {
		name string
		in   app.ScimUserInput
	}{
		{name: "missing externalId", in: app.ScimUserInput{UserName: "alice@corp.example"}},
		{name: "missing userName", in: app.ScimUserInput{ExternalID: "ext-1"}},
		{name: "userName too long", in: app.ScimUserInput{
			ExternalID: "ext-1", UserName: strings.Repeat("a", domain.MaxSubjectLen+1),
		}},
		{name: "invalid displayName", in: app.ScimUserInput{
			ExternalID: "ext-1", UserName: "alice@corp.example", DisplayName: "bad\nname",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := fixture.svc.CreateUser(ctx, tc.in, "trace-invalid"); err == nil {
				t.Fatal("expected 400 invalidValue")
			} else {
				requireScimError(t, err, 400)
			}
		})
	}

	// userName 唯一性：不同 externalId 复用同一主体返回 409。
	mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example",
	})
	if _, err := fixture.svc.CreateUser(ctx, app.ScimUserInput{
		ExternalID: "ext-2", UserName: "ALICE@corp.example",
	}, "trace-dup"); err == nil {
		t.Fatal("duplicate userName must fail")
	} else {
		scimErr := scimErrorOf(t, err, 409)
		if scimErr.ScimType != "uniqueness" {
			t.Fatalf("scimType = %q, want uniqueness", scimErr.ScimType)
		}
	}

	// 超长展示名按 rune 截断，不整批失败。
	longName := strings.Repeat("名", domain.MaxDisplayNameLen+6)
	truncated, err := fixture.svc.CreateUser(ctx, app.ScimUserInput{
		ExternalID: "ext-3", UserName: "long@corp.example", DisplayName: longName,
	}, "trace-long")
	if err != nil {
		t.Fatalf("create user with long displayName: %v", err)
	}
	if got := len([]rune(truncated.DisplayName)); got != domain.MaxDisplayNameLen {
		t.Fatalf("displayName rune length = %d, want %d", got, domain.MaxDisplayNameLen)
	}

	// active=false 直接建出停用账号（无本地口令，仅可经 SSO 登录）。
	disabled, err := fixture.svc.CreateUser(ctx, app.ScimUserInput{
		ExternalID: "ext-4", UserName: "carol@corp.example", Active: boolPtr(false),
	}, "trace-disabled")
	if err != nil {
		t.Fatalf("create disabled user: %v", err)
	}
	if disabled.Active {
		t.Fatal("user must be disabled")
	}
	account, err := fixture.accounts.FindByID(ctx, disabled.ID)
	if err != nil {
		t.Fatalf("load disabled account: %v", err)
	}
	if account.Status != domain.AccountStatusDisabled || account.TokenVersion != 2 {
		t.Fatalf("account = %+v, want disabled with token version 2", account)
	}
}

// TestScimDeleteUserRemovesMembershipAndRoles 验证删除撤销组成员关系与角色投影。
func TestScimDeleteUserRemovesMembershipAndRoles(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	user := mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example",
	})
	group := mustCreateScimGroup(t, fixture.svc, app.ScimGroupInput{
		ExternalID: "grp-1", DisplayName: "ops", Members: []string{"ext-1"},
	})
	if got := fixture.provisioner.grantedRoles(user.ID); len(got) != 1 || got[0] != "ops_admin" {
		t.Fatalf("granted = %v, want [ops_admin]", got)
	}

	if err := fixture.svc.DeleteUser(ctx, user.ID, "trace-delete"); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	account, err := fixture.accounts.FindByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("load deleted account: %v", err)
	}
	if !account.IsDeleted() || account.Status != domain.AccountStatusDisabled {
		t.Fatalf("account = %+v, want soft deleted and disabled", account)
	}
	members, err := fixture.groups.ListMembers(ctx, group.ID)
	if err != nil {
		t.Fatalf("list group members: %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("members = %v, want none after user delete", members)
	}
	if got := fixture.provisioner.revokedRoles(user.ID); len(got) != 1 || got[0] != "ops_admin" {
		t.Fatalf("revoked = %v, want [ops_admin]", got)
	}
	if _, err := fixture.svc.GetUser(ctx, user.ID); err == nil {
		t.Fatal("deleted user must be 404")
	}
}

// TestScimDeleteUserNotFoundAndChannelGuard 验证删除的 404 分支与端别边界。
func TestScimDeleteUserNotFoundAndChannelGuard(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	foreign := seedChannelAccount(t, fixture.accounts, domain.ChannelC)

	cases := []struct {
		name string
		id   string
	}{
		{name: "empty id", id: "  "},
		{name: "unknown id", id: "999"},
		{name: "non admin channel", id: foreign.ID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := fixture.svc.DeleteUser(ctx, tc.id, "trace-delete"); err == nil {
				t.Fatal("expected 404")
			} else {
				requireScimError(t, err, 404)
			}
		})
	}

	// 仓储故障原样上抛，不伪装成 404。
	storeErr := errors.New("account store down")
	accounts := &failingAccounts{
		AccountRepository: persistence.NewMemoryAccountRepository(),
		findErr:           storeErr,
	}
	svc := newScimTestService(t, func(cfg *app.ScimServiceConfig) { cfg.Accounts = accounts })
	if err := svc.DeleteUser(ctx, "42", "trace-delete"); !errors.Is(err, storeErr) {
		t.Fatalf("delete error = %v, want %v", err, storeErr)
	}
}

// TestScimUserAuditEvents 验证用户用例的审计事件与脱敏属性。
func TestScimUserAuditEvents(t *testing.T) {
	recorder := &recordingAudit{}
	svc := newScimTestService(t, func(cfg *app.ScimServiceConfig) { cfg.Audit = recorder })
	ctx := context.Background()

	externalID := "ext-0123456789abcdef0123456789abcdef"
	created, err := svc.CreateUser(ctx, app.ScimUserInput{
		ExternalID: externalID, UserName: "alice@corp.example", DisplayName: "Alice",
	}, "trace-create")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	events := recorder.recorded()
	if len(events) != 1 || events[0].Action != audit.ActionAuthScimUserCreated {
		t.Fatalf("events = %+v, want one created event", events)
	}
	createdEvent := events[0]
	if createdEvent.ActorType != "system" || createdEvent.Service != "identity-test" || createdEvent.TraceID != "trace-create" {
		t.Fatalf("created event meta = %+v", createdEvent)
	}
	// 审计属性不得携带完整外部 ID，只保留截断摘要。
	if hash := createdEvent.Attrs["external_id_hash"]; len(hash) != 16 || hash == externalID {
		t.Fatalf("external_id_hash = %q, want 16-char digest", hash)
	}
	if createdEvent.Attrs["active"] != "true" {
		t.Fatalf("active attr = %q, want true", createdEvent.Attrs["active"])
	}

	if _, err := svc.ReplaceUser(ctx, created.ID, app.ScimUserInput{
		ExternalID: externalID, UserName: "alice@corp.example", DisplayName: "Alice", Active: boolPtr(false),
	}, nil, "trace-disable"); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	current, err := svc.GetUser(ctx, created.ID)
	if err != nil {
		t.Fatalf("get disabled user: %v", err)
	}
	if _, err := svc.ReplaceUser(ctx, created.ID, app.ScimUserInput{
		ExternalID: externalID, UserName: "alice@corp.example", DisplayName: "Alice Two", Active: boolPtr(true),
	}, int64Ptr(current.Version), "trace-enable"); err != nil {
		t.Fatalf("enable user: %v", err)
	}
	if _, err := svc.ReplaceUser(ctx, created.ID, app.ScimUserInput{
		ExternalID: externalID, UserName: "alice@corp.example", DisplayName: "Alice Three",
	}, nil, "trace-update"); err != nil {
		t.Fatalf("update user: %v", err)
	}
	if err := svc.DeleteUser(ctx, created.ID, "trace-delete"); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	events = recorder.recorded()
	actions := make([]string, 0, len(events))
	for _, event := range events {
		actions = append(actions, event.Action)
	}
	want := []string{
		audit.ActionAuthScimUserCreated,
		audit.ActionAuthScimUserDeact,
		audit.ActionAuthScimUserUpdated,
		audit.ActionAuthScimUserUpdated,
		audit.ActionAuthScimUserDeleted,
	}
	if len(actions) != len(want) {
		t.Fatalf("actions = %v, want %v", actions, want)
	}
	for i := range want {
		if actions[i] != want[i] {
			t.Fatalf("actions = %v, want %v", actions, want)
		}
	}
	if events[2].Attrs["version_check"] != "if-match" {
		t.Fatalf("version_check = %q, want if-match", events[2].Attrs["version_check"])
	}
	if events[3].Attrs["version_check"] != "none" {
		t.Fatalf("version_check = %q, want none", events[3].Attrs["version_check"])
	}
	if events[1].Attrs["active"] != "false" {
		t.Fatalf("deactivated active attr = %q, want false", events[1].Attrs["active"])
	}
	if events[4].Attrs["provider"] != "okta" {
		t.Fatalf("deleted provider attr = %q, want okta", events[4].Attrs["provider"])
	}
}

// seedChannelAccountAs 直接写入指定端别 + 主体的活跃账号。
func seedChannelAccountAs(
	t *testing.T,
	accounts domain.AccountRepository,
	channel domain.Channel,
	subject string,
) *domain.Account {
	t.Helper()
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID:      "seed-" + string(channel) + "-" + subject,
		Channel: channel,
		Subject: subject,
		Now:     scimTestNow,
	})
	if err != nil {
		t.Fatalf("new %s account: %v", channel, err)
	}
	if err := account.Activate(scimTestNow); err != nil {
		t.Fatalf("activate %s account: %v", channel, err)
	}
	if err := accounts.Save(context.Background(), account, 0); err != nil {
		t.Fatalf("save %s account: %v", channel, err)
	}
	return account
}

// failingTokens 包装 SCIM 令牌仓储，按需注入查询故障。
type failingTokens struct {
	domain.ScimTokenRepository
	findErr error
}

// FindByID 注入故障时返回错误，否则委托给内存仓储。
func (f *failingTokens) FindByID(ctx context.Context, id string) (*domain.ScimToken, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.ScimTokenRepository.FindByID(ctx, id)
}

// TestScimMutationRepositoryFailures 验证写路径的依赖故障原样上抛（fail-closed）。
func TestScimMutationRepositoryFailures(t *testing.T) {
	ctx := context.Background()
	storeErr := errors.New("account store down")
	memAccounts := persistence.NewMemoryAccountRepository()
	account := seedChannelAccountAs(t, memAccounts, domain.ChannelAdmin, "alice@corp.example")

	// CreateUser：账号主体查询故障上抛。
	createSvc := newScimTestService(t, func(cfg *app.ScimServiceConfig) {
		cfg.Accounts = &failingAccounts{AccountRepository: memAccounts, findSubjectErr: storeErr}
	})
	if _, err := createSvc.CreateUser(ctx, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "bob@corp.example",
	}, "trace-create"); !errors.Is(err, storeErr) {
		t.Fatalf("create user error = %v, want %v", err, storeErr)
	}

	// CreateUser：外部身份仓储不可用时按 503 返回。
	identitySvc := newScimTestService(t, func(cfg *app.ScimServiceConfig) {
		cfg.Identities = &failingIdentities{
			OAuthIdentityRepository: persistence.NewMemoryOAuthIdentityRepository(),
			findErr:                 errors.New("identity store down"),
		}
	})
	if _, err := identitySvc.CreateUser(ctx, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "bob@corp.example",
	}, "trace-create"); err == nil {
		t.Fatal("expected unavailable")
	} else {
		requireAppCode(t, err, apperr.CodeServiceUnavailable)
	}

	// ReplaceUser：改 userName 时主体查询故障上抛。
	updateSvc := newScimTestService(t, func(cfg *app.ScimServiceConfig) {
		cfg.Accounts = &failingAccounts{AccountRepository: memAccounts, findSubjectErr: storeErr}
	})
	if _, err := updateSvc.ReplaceUser(ctx, account.ID, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "new@corp.example", DisplayName: "Alice",
	}, nil, "trace-update"); !errors.Is(err, storeErr) {
		t.Fatalf("replace user error = %v, want %v", err, storeErr)
	}

	// ReplaceUser：外部身份读取故障时按 503 返回。
	externalSvc := newScimTestService(t, func(cfg *app.ScimServiceConfig) {
		cfg.Accounts = memAccounts
		cfg.Identities = &failingIdentities{
			OAuthIdentityRepository: persistence.NewMemoryOAuthIdentityRepository(),
			listErr:                 errors.New("identity store down"),
		}
	})
	if _, err := externalSvc.ReplaceUser(ctx, account.ID, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example", DisplayName: "Alice",
	}, nil, "trace-update"); err == nil {
		t.Fatal("expected unavailable")
	} else {
		requireAppCode(t, err, apperr.CodeServiceUnavailable)
	}

	// DeleteUser：组成员读取故障上抛。
	_, realGroups := persistence.NewMemoryScimRepositories()
	deleteSvc := newScimTestService(t, func(cfg *app.ScimServiceConfig) {
		cfg.Accounts = memAccounts
		cfg.Groups = &failingGroups{ScimGroupRepository: realGroups, listErr: storeErr}
	})
	if err := deleteSvc.DeleteUser(ctx, account.ID, "trace-delete"); !errors.Is(err, storeErr) {
		t.Fatalf("delete user error = %v, want %v", err, storeErr)
	}
}

// TestScimTokenRevokeAndErrorRendering 验证令牌吊销的幂等、404 与故障分支。
func TestScimTokenRevokeAndErrorRendering(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	token, err := fixture.svc.CreateToken(ctx, "1", "trace-token")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	scimErr := scimErrorOf(t, fixture.svc.RevokeToken(ctx, "999", "1", "trace-revoke"), 404)
	if scimErr.Error() == "" {
		t.Fatal("scim error must render a message")
	}
	if err := fixture.svc.RevokeToken(ctx, token.Token.ID, "1", "trace-revoke"); err != nil {
		t.Fatalf("revoke token: %v", err)
	}
	if err := fixture.svc.RevokeToken(ctx, token.Token.ID, "1", "trace-revoke-again"); err != nil {
		t.Fatalf("repeat revoke must be idempotent: %v", err)
	}

	storeErr := errors.New("token store down")
	tokens, _ := persistence.NewMemoryScimRepositories()
	brokenSvc := newScimTestService(t, func(cfg *app.ScimServiceConfig) {
		cfg.Tokens = &failingTokens{ScimTokenRepository: tokens, findErr: storeErr}
	})
	if err := brokenSvc.RevokeToken(ctx, "1", "1", "trace-revoke"); !errors.Is(err, storeErr) {
		t.Fatalf("revoke token error = %v, want %v", err, storeErr)
	}
}
