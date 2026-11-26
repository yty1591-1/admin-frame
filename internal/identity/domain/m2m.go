package domain

import (
	"context"
	"strings"
	"time"
)

// M2M 客户端字段上限，与 migrations/000028 的列宽一致。
const (
	// MaxM2MClientIDLen client_id 最大字节数（svc_ + 16 字节 base64url）。
	MaxM2MClientIDLen = 64
	// MaxM2MNameLen 客户端显示名最大 rune 数。
	MaxM2MNameLen = 128
	// MaxM2MOwnerLen 归属方（团队 / 系统）最大字节数。
	MaxM2MOwnerLen = 64
	// MaxM2MScopeLen 单个权限码最大字节数。
	MaxM2MScopeLen = 128
	// MaxM2MScopes 单客户端允许申请的权限码数量上限。
	MaxM2MScopes = 64
	// MaxM2MSecretsPerClient 每客户端最多有效 secret 数（current + retiring，§21.2.4）。
	MaxM2MSecretsPerClient = 2
)

// M2MClientStatus 是机器客户端生命周期状态。
type M2MClientStatus string

const (
	// M2MClientActive 可签发令牌。
	M2MClientActive M2MClientStatus = "active"
	// M2MClientDisabled 已停用；拒绝签发，旧令牌按 token_version 失效。
	M2MClientDisabled M2MClientStatus = "disabled"
	// M2MClientDeleted 已软删；client_id 不可复用。
	M2MClientDeleted M2MClientStatus = "deleted"
)

// Validate 校验客户端状态取值。
func (s M2MClientStatus) Validate() error {
	switch s {
	case M2MClientActive, M2MClientDisabled, M2MClientDeleted:
		return nil
	default:
		return errInvalid(ErrInvalidM2MClient, "m2m client status %q is not supported", string(s))
	}
}

// String 返回状态字面量。
func (s M2MClientStatus) String() string { return string(s) }

// M2MSecretStatus 是客户端密钥状态。
type M2MSecretStatus string

const (
	// M2MSecretCurrent 当前生效密钥；新签发令牌优先使用。
	M2MSecretCurrent M2MSecretStatus = "current"
	// M2MSecretRetiring 轮换后进入宽限期的旧密钥；宽限内仍可认证。
	M2MSecretRetiring M2MSecretStatus = "retiring"
	// M2MSecretRevoked 已吊销；立即不可用。
	M2MSecretRevoked M2MSecretStatus = "revoked"
)

// Validate 校验密钥状态取值。
func (s M2MSecretStatus) Validate() error {
	switch s {
	case M2MSecretCurrent, M2MSecretRetiring, M2MSecretRevoked:
		return nil
	default:
		return errInvalid(ErrInvalidM2MSecret, "m2m secret status %q is not supported", string(s))
	}
}

// String 返回状态字面量。
func (s M2MSecretStatus) String() string { return string(s) }

// M2MClient 是服务间机器身份聚合根（docs/identity-authn.md §21.2）。
//
// 不变量：
//   - client_id 全局唯一且软删后不可复用；
//   - token_version ≥ 1 且只增不减；停用、启用、轮换、缩小 scope 均递增，
//     使旧令牌在 state_ttl（≤60s）内失效；
//   - allowed_channels 非空且 ⊆ {admin,biz,c}；allowed_scopes 非空、无通配符；
//   - 不绑定任何人类主体：不得登录 UI，也不得代表用户访问用户资源。
type M2MClient struct {
	// ID 雪花 ID（内部主键）。
	ID string
	// ClientID 对外客户端标识，形如 svc_<16 字节 base64url>。
	ClientID string
	// Name 展示名，仅用于列表与审计。
	Name string
	// Status 生命周期状态。
	Status M2MClientStatus
	// AllowedChannels 允许取令牌的端别；一次只为其中一端签发。
	AllowedChannels []Channel
	// AllowedScopes 允许申请的权限码白名单（精确匹配，禁止通配）。
	AllowedScopes []string
	// TokenVersion 撤销版本，签发时写入 ver，校验时比较。
	TokenVersion int64
	// Owner 归属团队 / 系统，便于轮换责任到人。
	Owner string
	// CreatedBy 创建人主体 ID（A 端账号）。
	CreatedBy string
	// Version 乐观锁版本。
	Version int64
	// RotationDueAt 最近一次 secret 轮换时间；超 max_secret_age 仅告警。
	RotationDueAt *time.Time
	// CreatedAt / UpdatedAt 审计时间戳（UTC）。
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt 软删时间；非空表示不可用且 client_id 不可复用。
	DeletedAt *time.Time
}

// NormalizeM2MClient 规范化客户端字段：去空白、去重、小写端别。
func NormalizeM2MClient(client *M2MClient) {
	if client == nil {
		return
	}
	client.ClientID = strings.TrimSpace(client.ClientID)
	client.Name = strings.TrimSpace(client.Name)
	client.Owner = strings.TrimSpace(client.Owner)
	client.CreatedBy = strings.TrimSpace(client.CreatedBy)
	channels := make([]Channel, 0, len(client.AllowedChannels))
	seenChannel := make(map[string]struct{}, len(client.AllowedChannels))
	for _, item := range client.AllowedChannels {
		name := strings.ToLower(strings.TrimSpace(string(item)))
		if name == "" {
			continue
		}
		if _, dup := seenChannel[name]; dup {
			continue
		}
		seenChannel[name] = struct{}{}
		channels = append(channels, Channel(name))
	}
	client.AllowedChannels = channels
	scopes := make([]string, 0, len(client.AllowedScopes))
	seenScope := make(map[string]struct{}, len(client.AllowedScopes))
	for _, item := range client.AllowedScopes {
		scope := strings.TrimSpace(item)
		if scope == "" {
			continue
		}
		if _, dup := seenScope[scope]; dup {
			continue
		}
		seenScope[scope] = struct{}{}
		scopes = append(scopes, scope)
	}
	client.AllowedScopes = scopes
}

// Validate 校验客户端聚合不变量；错误信息不含密钥材料。
func (c *M2MClient) Validate() error {
	if c == nil {
		return errInvalid(ErrInvalidM2MClient, "m2m client is required")
	}
	if strings.TrimSpace(c.ClientID) == "" {
		return errInvalid(ErrEmptyInput, "m2m client_id is required")
	}
	if len(c.ClientID) > MaxM2MClientIDLen {
		return errInvalid(ErrInvalidM2MClient, "m2m client_id must be <= %d bytes", MaxM2MClientIDLen)
	}
	if strings.TrimSpace(c.Name) == "" {
		return errInvalid(ErrEmptyInput, "m2m client name is required")
	}
	if len([]rune(c.Name)) > MaxM2MNameLen {
		return errInvalid(ErrInvalidM2MClient, "m2m client name must be <= %d runes", MaxM2MNameLen)
	}
	if err := c.Status.Validate(); err != nil {
		return err
	}
	if c.TokenVersion < 1 {
		return errInvalid(ErrInvalidM2MClient, "m2m client token_version must be >= 1")
	}
	if len(c.AllowedChannels) == 0 {
		return errInvalid(ErrInvalidM2MClient, "m2m client allowed_channels must not be empty")
	}
	for _, channel := range c.AllowedChannels {
		if err := channel.Validate(); err != nil {
			return err
		}
	}
	if len(c.AllowedScopes) == 0 {
		return errInvalid(ErrInvalidM2MClient, "m2m client allowed_scopes must not be empty")
	}
	if len(c.AllowedScopes) > MaxM2MScopes {
		return errInvalid(ErrInvalidM2MClient, "m2m client allowed_scopes must be <= %d entries", MaxM2MScopes)
	}
	for _, scope := range c.AllowedScopes {
		if len(scope) > MaxM2MScopeLen {
			return errInvalid(ErrInvalidM2MClient, "m2m scope must be <= %d bytes", MaxM2MScopeLen)
		}
		if strings.Contains(scope, "*") {
			return errInvalid(ErrInvalidM2MClient, "m2m scope must not contain wildcard")
		}
	}
	if len(c.Owner) > MaxM2MOwnerLen {
		return errInvalid(ErrInvalidM2MClient, "m2m owner must be <= %d bytes", MaxM2MOwnerLen)
	}
	return nil
}

// Active 报告客户端是否可签发令牌。
func (c *M2MClient) Active() bool {
	return c != nil && c.Status == M2MClientActive && c.DeletedAt == nil
}

// AllowsChannel 报告客户端是否被允许为指定端别取令牌。
func (c *M2MClient) AllowsChannel(channel Channel) bool {
	if c == nil {
		return false
	}
	for _, item := range c.AllowedChannels {
		if item == channel {
			return true
		}
	}
	return false
}

// AllowsScope 报告权限码是否在客户端白名单内（精确匹配）。
func (c *M2MClient) AllowsScope(scope string) bool {
	if c == nil {
		return false
	}
	target := strings.TrimSpace(scope)
	for _, item := range c.AllowedScopes {
		if item == target {
			return true
		}
	}
	return false
}

// M2MSecret 是客户端密钥；库内只存 SHA-256 摘要与指纹，明文只在创建 / 轮换响应出现一次。
type M2MSecret struct {
	// ID 雪花 ID。
	ID string
	// ClientID 所属客户端对外标识。
	ClientID string
	// SecretHash SHA-256 十六进制摘要；全局唯一。
	SecretHash string
	// Fingerprint 供展示的摘要前缀（如 sha256:ab12cd），不可反推明文。
	Fingerprint string
	// Status current / retiring / revoked。
	Status M2MSecretStatus
	// CreatedAt 创建时间。
	CreatedAt time.Time
	// ExpiresAt retiring 状态的宽限到期时间；到期后拒绝认证。
	ExpiresAt *time.Time
	// RevokedAt 吊销时间。
	RevokedAt *time.Time
}

// Validate 校验密钥字段。
func (s *M2MSecret) Validate() error {
	if s == nil {
		return errInvalid(ErrInvalidM2MSecret, "m2m secret is required")
	}
	if strings.TrimSpace(s.ClientID) == "" {
		return errInvalid(ErrEmptyInput, "m2m secret client_id is required")
	}
	if strings.TrimSpace(s.SecretHash) == "" {
		return errInvalid(ErrEmptyInput, "m2m secret hash is required")
	}
	return s.Status.Validate()
}

// Usable 报告密钥在指定时间是否可用于认证。
//
// current 立即可用；retiring 在宽限到期前可用；revoked 一律不可用。
func (s *M2MSecret) Usable(now time.Time) bool {
	if s == nil {
		return false
	}
	switch s.Status {
	case M2MSecretCurrent:
		return s.RevokedAt == nil
	case M2MSecretRetiring:
		if s.RevokedAt != nil {
			return false
		}
		return s.ExpiresAt == nil || now.Before(*s.ExpiresAt)
	default:
		return false
	}
}

// M2MClientRepository 是机器客户端聚合的持久化端口。
//
// 约定：
//   - 未找到返回 apperr.NotFound；
//   - Save 使用 expectedVersion 做乐观锁，冲突返回 apperr.Conflict；
//   - expectedVersion=0 表示创建；
//   - client_id 软删后仍占用唯一键，禁止复用。
type M2MClientRepository interface {
	// FindByClientID 按对外标识加载客户端；软删客户端仍返回，用于阻止 client_id 复用。
	FindByClientID(ctx context.Context, clientID string) (*M2MClient, error)
	// FindByID 按主键加载客户端。
	FindByID(ctx context.Context, id string) (*M2MClient, error)
	// Save 创建或更新客户端。
	Save(ctx context.Context, client *M2MClient, expectedVersion int64) error
	// List 分页列出客户端（含软删），返回当页与总数。
	List(ctx context.Context, filter M2MClientListFilter) ([]*M2MClient, int, error)
}

// M2MClientListFilter 是客户端列表查询条件。
type M2MClientListFilter struct {
	// Status 状态过滤；空值表示不过滤。
	Status M2MClientStatus
	// Channel 端别过滤；空值表示不过滤（匹配 allowed_channels 包含该端别的客户端）。
	Channel Channel
	// Limit 每页条数。
	Limit int
	// Offset 偏移量。
	Offset int
}

// M2MSecretRepository 是客户端密钥的持久化端口。
//
// 唯一约束为 secret_hash；同一客户端最多 2 个有效（current + retiring）密钥，
// 由用例在事务内校验（§21.2.4）。
type M2MSecretRepository interface {
	// ListByClient 返回客户端全部密钥，按创建时间倒序。
	ListByClient(ctx context.Context, clientID string) ([]*M2MSecret, error)
	// FindByHash 按摘要加载密钥；不存在返回 apperr.NotFound。
	FindByHash(ctx context.Context, secretHash string) (*M2MSecret, error)
	// Save 创建密钥；摘要冲突返回 apperr.Conflict。
	Save(ctx context.Context, secret *M2MSecret) error
	// UpdateStatus 更新密钥状态（current → retiring / revoked）与到期时间。
	UpdateStatus(ctx context.Context, secret *M2MSecret) error
	// RevokeAll 吊销客户端全部未吊销密钥，返回受影响条数。
	RevokeAll(ctx context.Context, clientID string, now time.Time) (int, error)
}
