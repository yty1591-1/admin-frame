// Package app 承载 IAM 的应用服务：用例编排、事务边界与端口组合。
//
// 依赖方向固定为 app → domain（端口）+ infrastructure（实现）；app 不得
// import transport，也不得直接拼 SQL。鉴权路径的服务（AuthorizeService）
// 实现 internal/security.SnapshotLoader，由 cmd 注入后交给 AuthZ 中间件。
package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	iampcache "github.com/admin-frame/admin-frame/internal/iam/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/authz"
	pkgcache "github.com/admin-frame/admin-frame/pkg/cache"
)

const (
	// DefaultLoadTimeout 是回源 DB 的默认超时（docs/rbac-iam.md §8.2）。
	DefaultLoadTimeout = 300 * time.Millisecond
	// DefaultMaxRoles 是单主体快照允许的最大角色数（iam.limits.max_roles_per_subject）。
	DefaultMaxRoles = 20
	// DefaultMaxPermissions 是单主体快照允许的最大权限数（防御角色膨胀）。
	DefaultMaxPermissions = 2000
)

// errSnapshotLimit 表示快照规模超过配置上限；属于数据异常，必须失败关闭。
var (
	errSnapshotLimit   = errors.New("iam snapshot exceeds configured limits")
	errSnapshotSubject = errors.New("iam snapshot subject mismatch")
)

// Observer 观测快照加载；实现须并发安全且不得阻塞请求。
type Observer interface {
	// OnSnapshotLoad 记录一次加载耗时与来源（l1 / l2 / db）。
	OnSnapshotLoad(ctx context.Context, channel string, source iampcache.Layer, elapsed time.Duration)
	// OnSnapshot 记录快照规模，用于发现角色膨胀。
	OnSnapshot(ctx context.Context, channel string, permissions int)
	// OnSnapshotError 记录加载 / 缓存写失败；stage 取 cache / db / cache_write / limit。
	OnSnapshotError(ctx context.Context, stage string, err error)
}

// NopObserver 是不做任何观测的默认实现。
type NopObserver struct{}

// OnSnapshotLoad 实现 Observer。
func (NopObserver) OnSnapshotLoad(context.Context, string, iampcache.Layer, time.Duration) {}

// OnSnapshot 实现 Observer。
func (NopObserver) OnSnapshot(context.Context, string, int) {}

// OnSnapshotError 实现 Observer。
func (NopObserver) OnSnapshotError(context.Context, string, error) {}

// AuthorizeConfig 是 AuthorizeService 的装配参数。
type AuthorizeConfig struct {
	// Repository 快照只读端口（必填）。
	Repository domain.SnapshotRepository
	// Cache 两级快照缓存（必填）。
	Cache *iampcache.SnapshotCache
	// Env 环境前缀，进入缓存 key；local / test 可为空。
	Env string
	// LoadTimeout 回源 DB 超时；≤0 使用 DefaultLoadTimeout。
	LoadTimeout time.Duration
	// MaxRoles 单主体最大角色数；≤0 使用 DefaultMaxRoles。
	MaxRoles int
	// MaxPermissions 单主体最大权限数；≤0 使用 DefaultMaxPermissions。
	MaxPermissions int
	// AllowWildcard 是否保留通配符权限（prod 必须为 false；breakglass 展开在 M4）。
	AllowWildcard bool
	// Observer 可选观测器。
	Observer Observer
}

// AuthorizeService 加载主体有效权限快照并实现 security.SnapshotLoader。
//
// 并发未命中通过 singleflight 合并为一次回源；跨实例击穿由短 L1 TTL、
// L2 与 negative_ttl 兜底（§8.2）。
type AuthorizeService struct {
	repository     domain.SnapshotRepository
	cache          *iampcache.SnapshotCache
	env            string
	loadTimeout    time.Duration
	maxRoles       int
	maxPermissions int
	allowWildcard  bool
	observer       Observer
	group          singleflight.Group
}

// NewAuthorizeService 校验必填依赖并构造服务。
func NewAuthorizeService(cfg AuthorizeConfig) (*AuthorizeService, error) {
	if cfg.Repository == nil {
		return nil, apperr.Internal("iam authorize service: snapshot repository is required", nil)
	}
	if cfg.Cache == nil {
		return nil, apperr.Internal("iam authorize service: snapshot cache is required", nil)
	}
	if cfg.LoadTimeout < 0 {
		return nil, apperr.Internal("iam authorize service: load timeout must be > 0", nil)
	}
	if cfg.MaxRoles < 0 || cfg.MaxPermissions < 0 {
		return nil, apperr.Internal("iam authorize service: limits must be > 0", nil)
	}
	timeout := cfg.LoadTimeout
	if timeout == 0 {
		timeout = DefaultLoadTimeout
	}
	maxRoles := cfg.MaxRoles
	if maxRoles == 0 {
		maxRoles = DefaultMaxRoles
	}
	maxPermissions := cfg.MaxPermissions
	if maxPermissions == 0 {
		maxPermissions = DefaultMaxPermissions
	}
	observer := cfg.Observer
	if observer == nil {
		observer = NopObserver{}
	}
	return &AuthorizeService{
		repository:     cfg.Repository,
		cache:          cfg.Cache,
		env:            cfg.Env,
		loadTimeout:    timeout,
		maxRoles:       maxRoles,
		maxPermissions: maxPermissions,
		allowWildcard:  cfg.AllowWildcard,
		observer:       observer,
	}, nil
}

// LoadSnapshot 实现 security.SnapshotLoader。
//
// 顺序：context → L1 → L2 → DB（超时 + singleflight）→ 回填 → 返回。
// 任一必要步骤失败返回错误，由中间件失败关闭；绝不回退到令牌中的角色。
func (s *AuthorizeService) LoadSnapshot(ctx context.Context, principal auth.Principal) (authz.Snapshot, error) {
	if s == nil {
		return authz.Snapshot{}, apperr.Unavailable("iam authorize service is not configured")
	}
	query, key, err := s.target(principal)
	if err != nil {
		return authz.Snapshot{}, err
	}
	if snapshot, ok := authz.FromContext(ctx); ok && matches(snapshot, query) {
		return snapshot.Clone(), nil
	}

	started := time.Now()
	if snapshot, layer, err := s.cache.Get(ctx, key); err == nil {
		s.observer.OnSnapshotLoad(ctx, snapshot.Channel, layer, time.Since(started))
		s.observer.OnSnapshot(ctx, snapshot.Channel, len(snapshot.Permissions))
		return snapshot, nil
	} else if !errors.Is(err, pkgcache.ErrMiss) {
		s.observer.OnSnapshotError(ctx, "cache", err)
		return authz.Snapshot{}, apperr.Unavailable("iam snapshot cache unavailable")
	}
	return s.loadFromRepository(ctx, key, query)
}

// loadFromRepository 在缓存未命中时回源；singleflight 合并同 key 并发请求。
func (s *AuthorizeService) loadFromRepository(ctx context.Context, key string, query domain.SubjectQuery) (authz.Snapshot, error) {
	value, err, _ := s.group.Do(key, func() (any, error) {
		started := time.Now()
		// 双检：等待期间可能已有其他请求回填缓存。
		if snapshot, layer, err := s.cache.Get(ctx, key); err == nil {
			s.observer.OnSnapshotLoad(ctx, snapshot.Channel, layer, time.Since(started))
			return snapshot, nil
		} else if !errors.Is(err, pkgcache.ErrMiss) {
			return nil, apperr.Unavailable("iam snapshot cache unavailable")
		}

		loadCtx, cancel := context.WithTimeout(ctx, s.loadTimeout)
		defer cancel()
		raw, err := s.repository.Load(loadCtx, query)
		if err != nil {
			s.observer.OnSnapshotError(ctx, "db", err)
			return nil, apperr.Unavailable("iam authorization dependency unavailable")
		}
		snapshot, err := s.build(raw)
		if err != nil {
			return nil, err
		}
		if !matches(snapshot, query) {
			s.observer.OnSnapshotError(ctx, "db", errSnapshotSubject)
			return nil, apperr.Unavailable(errSnapshotSubject.Error())
		}
		if err := s.cache.Set(ctx, key, snapshot); err != nil {
			// 缓存写失败不阻断本次鉴权，由 TTL / 告警兜底（REL-06）。
			s.observer.OnSnapshotError(ctx, "cache_write", err)
		}
		s.observer.OnSnapshotLoad(ctx, string(query.Channel), iampcache.LayerDB, time.Since(started))
		s.observer.OnSnapshot(ctx, snapshot.Channel, len(snapshot.Permissions))
		return snapshot, nil
	})
	if err != nil {
		return authz.Snapshot{}, err
	}
	snapshot, ok := value.(authz.Snapshot)
	if !ok {
		return authz.Snapshot{}, apperr.Unavailable("iam snapshot payload is invalid")
	}
	return snapshot.Clone(), nil
}

// target 校验 principal 并生成查询条件与缓存 key。
//
// 这里的 4xx 错误表示令牌身份与端别不匹配（SEC-03 / SEC-04），不是依赖故障；
// security 中间件会原样返回 401，而不是误报 503。
func (s *AuthorizeService) target(principal auth.Principal) (domain.SubjectQuery, string, error) {
	channel := domain.Channel(principal.Channel)
	subjectType, ok := subjectTypeFor(channel)
	if !ok {
		return domain.SubjectQuery{}, "", apperr.Unauthorized("iam: unsupported channel")
	}
	if principal.SubjectID == "" {
		return domain.SubjectQuery{}, "", apperr.Unauthorized("iam: subject is required")
	}
	if channel == domain.ChannelBiz && principal.MerchantID == "" {
		return domain.SubjectQuery{}, "", apperr.Unauthorized("iam: merchant is required")
	}
	if channel == domain.ChannelAdmin && principal.MerchantID != "" {
		return domain.SubjectQuery{}, "", apperr.Unauthorized("iam: admin subject must not carry merchant")
	}
	key, err := iampcache.SubjectKey(s.env, string(channel), principal.MerchantID, string(subjectType), principal.SubjectID)
	if err != nil {
		return domain.SubjectQuery{}, "", apperr.Internal("iam: invalid cache key", err)
	}
	return domain.SubjectQuery{
		Channel:     channel,
		SubjectType: subjectType,
		SubjectID:   principal.SubjectID,
		MerchantID:  principal.MerchantID,
		At:          time.Now().UTC(),
	}, key, nil
}

// subjectTypeFor 按端别推导主体类型；两端主体类型不可互换。
func subjectTypeFor(channel domain.Channel) (domain.SubjectType, bool) {
	switch channel {
	case domain.ChannelAdmin:
		return domain.SubjectStaff, true
	case domain.ChannelBiz:
		return domain.SubjectMerchantStaff, true
	default:
		return "", false
	}
}

// build 把仓储快照转换为共享契约，并落实清洗与上限保护。
func (s *AuthorizeService) build(raw *domain.SubjectSnapshot) (authz.Snapshot, error) {
	if raw == nil {
		return authz.Snapshot{}, apperr.Unavailable("iam snapshot payload is empty")
	}
	if len(raw.Roles) > s.maxRoles || len(raw.Permissions) > s.maxPermissions {
		s.observer.OnSnapshotError(context.Background(), "limit", errSnapshotLimit)
		return authz.Snapshot{}, apperr.Unavailable(errSnapshotLimit.Error())
	}

	roles := dedupeSorted(raw.Roles)
	permissions := make([]string, 0, len(raw.Permissions))
	for _, code := range raw.Permissions {
		value := string(code)
		// 非破玻璃快照不得携带通配符：只有快照标记 Breakglass 时才允许 "*"，
		// 与 iam.security.allow_wildcard（普通角色通配符）解耦，prod 也可用破玻璃。
		if !s.allowWildcard && strings.Contains(value, "*") && !raw.Breakglass {
			continue
		}
		permissions = append(permissions, value)
	}
	permissions = dedupeSorted(permissions)

	var scopes map[string]authz.Scope
	if len(raw.Scopes) > 0 {
		scopes = make(map[string]authz.Scope, len(raw.Scopes))
		for _, scope := range raw.Scopes {
			scopes[scope.Resource] = authz.Scope{
				Type:        string(scope.Type),
				MerchantIDs: append([]string(nil), scope.MerchantIDs...),
			}
		}
	}
	generatedAt := raw.GeneratedAt
	if generatedAt.IsZero() {
		generatedAt = time.Now().UTC()
	}
	return authz.Snapshot{
		Channel:     string(raw.Channel),
		SubjectType: string(raw.SubjectType),
		SubjectID:   raw.SubjectID,
		MerchantID:  raw.MerchantID,
		Roles:       roles,
		Permissions: permissions,
		Scopes:      scopes,
		Version:     raw.Version,
		GeneratedAt: generatedAt.UTC(),
	}, nil
}

// dedupeSorted 去重并排序，保证缓存值与测试断言稳定。
func dedupeSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

// matches 判断快照与查询主体是否一致，防止复用其他主体 / 租户的快照。
func matches(snapshot authz.Snapshot, query domain.SubjectQuery) bool {
	return snapshot.Channel == string(query.Channel) &&
		snapshot.SubjectType == string(query.SubjectType) &&
		snapshot.SubjectID == query.SubjectID &&
		snapshot.MerchantID == query.MerchantID
}
