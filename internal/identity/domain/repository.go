package domain

import (
	"context"
	"time"
)

// AccountRepository 是账号聚合的持久化端口，由 infrastructure 实现。
//
// 约定：
//   - 未找到返回 apperr.NotFound（errors.Is(err, apperr.ErrNotFound)）；
//   - Save 必须使用 expectedVersion 做乐观锁，冲突返回 apperr.Conflict；
//   - Save 的 expectedVersion 为 0 表示创建；
//   - channel / merchant_id 创建后不可变，仓储须拒绝变更；
//   - subject 只能通过 RebindSubject 变更（C 端手机号换绑），Save 须拒绝；
//   - 读方法返回副本，调用方修改返回值不得影响仓储内部状态。
type AccountRepository interface {
	// FindByID 按主键加载账号。
	FindByID(ctx context.Context, id string) (*Account, error)
	// FindBySubject 按端别 + 商户 + 规范化主体加载账号；软删账号仍返回，用于阻止 subject 复用。
	//
	// B 端以 merchant_id 隔离租户；A/C 端 merchant_id 固定为空串。
	FindBySubject(ctx context.Context, channel Channel, merchantID, subject string) (*Account, error)
	// Save 创建或更新账号；expectedVersion 为 0 表示创建。
	Save(ctx context.Context, account *Account, expectedVersion int64) error
	// List 按端别 / 状态 / 关键词分页列出账号（含软删），返回当页与总数。
	List(ctx context.Context, filter AccountListFilter) ([]*Account, int, error)
	// RebindSubject 更新登录主体；expectedVersion 用于乐观锁。
	RebindSubject(ctx context.Context, account *Account, expectedVersion int64) error
}

// AccountListFilter 是账号列表查询条件。
//
// 只允许 A 端运营查询；关键词匹配 subject 子串，禁止用于 B/C 端自助接口。
type AccountListFilter struct {
	// Channel 端别；必填。
	Channel Channel
	// Status 状态过滤；空值表示不过滤。
	Status AccountStatus
	// Keyword 主体关键词；空值表示不过滤。
	Keyword string
	// SubjectExact 规范化后的主体精确值；非空时优先于 Keyword 并走 subject_index 索引（PERF-07）。
	SubjectExact string
	// MerchantID B 端商户过滤；空值表示不按商户过滤（A/C 端固定为空串）。
	MerchantID string
	// Limit 每页条数；调用方负责上限钳制。
	Limit int
	// Offset 偏移量；按 created_at、id 倒序。
	Offset int
}

// CredentialRepository 是凭据的持久化端口。
//
// 唯一约束为 (account_id, credential_type)：同一账号同一类型仅一条有效凭据。
type CredentialRepository interface {
	// FindByAccount 返回账号指定类型的凭据；不存在返回 apperr.NotFound。
	FindByAccount(ctx context.Context, accountID string, typ CredentialType) (*Credential, error)
	// ListByAccount 返回账号全部凭据，按类型排序。
	ListByAccount(ctx context.Context, accountID string) ([]*Credential, error)
	// Save 创建或替换凭据；同账号同类型以调用方提供的最新哈希为准。
	Save(ctx context.Context, credential *Credential) error
	// Delete 删除凭据；不存在也返回成功（幂等）。
	Delete(ctx context.Context, accountID string, typ CredentialType) error
}

// SessionRepository 是会话的持久化端口。
//
// 约定：
//   - RefreshHash 全局唯一，只存 SHA-256 摘要；
//   - Save 必须使用 expectedVersion 做乐观锁，保证并发刷新只有一个请求成功；
//   - RevokeFamily 在同一语句内把整条会话链置为 revoked（含已 rotated 的会话），并同步撤销设备会话；
//   - RevokeByAccount 撤销账号下全部会话，并同步撤销全部设备会话。
type SessionRepository interface {
	// FindByID 按主键加载会话；不存在返回 apperr.NotFound。
	FindByID(ctx context.Context, id string) (*Session, error)
	// FindByRefreshHash 按刷新令牌摘要加载会话；不存在返回 apperr.NotFound。
	FindByRefreshHash(ctx context.Context, hash string) (*Session, error)
	// Save 创建或更新会话；expectedVersion 为 0 表示创建。
	Save(ctx context.Context, session *Session, expectedVersion int64) error
	// RevokeFamily 撤销一个家庭下所有 active / rotated 会话，返回受影响条数。
	RevokeFamily(ctx context.Context, familyID string, reason RevokeReason, now time.Time) (int, error)
	// RevokeByAccount 撤销账号下全部 active / rotated 会话（logout-all / 停用）。
	RevokeByAccount(ctx context.Context, accountID string, reason RevokeReason, now time.Time) (int, error)
	// ListByAccount 返回账号全部会话，按创建时间倒序，供审计与清理使用。
	ListByAccount(ctx context.Context, accountID string) ([]*Session, error)
	// FindFamily 按主键加载设备会话；不存在返回 apperr.NotFound。
	FindFamily(ctx context.Context, familyID string) (*SessionFamily, error)
	// SaveFamily 创建设备会话（expectedVersion=0）或使用乐观锁更新。
	SaveFamily(ctx context.Context, family *SessionFamily, expectedVersion int64) error
	// ListFamiliesByAccount 按 last_used_at / family_id 倒序返回设备会话（游标分页）。
	//
	// afterLastUsed / afterID 为空表示第一页；limit <= 0 使用调用方默认值。
	ListFamiliesByAccount(ctx context.Context, accountID string, afterLastUsed time.Time, afterID string, limit int) ([]*SessionFamily, error)
	// ListActiveByFamily 返回 family 下仍可能持有有效 Access Token 的会话（active / rotated）。
	//
	// activeSince 由调用方按 access_ttl 计算；实现必须按 last_used_at 过滤并设置硬上限，
	// 禁止加载整条会话链历史（IDN-GAP-PERF-03）。
	ListActiveByFamily(ctx context.Context, familyID string, activeSince time.Time) ([]*Session, error)
	// ExpireStale 把到期的会话与设备标记为 expired，单次最多 limit 条。
	ExpireStale(ctx context.Context, now time.Time, limit int) (SessionExpireResult, error)
	// DeleteExpiredBefore 删除 before 之前的 rotated / revoked / expired 会话与设备，单次最多 limit 条。
	DeleteExpiredBefore(ctx context.Context, before time.Time, limit int) (SessionDeleteResult, error)
	// ArchiveExpiredBefore 把 before 之前已失效的会话与设备移入归档表，单次最多 limit 条。
	//
	// 必须在同一事务内完成「写归档 + 删在线」，避免中断后出现只归档未删除的半状态；
	// archivedAt 由调用方传入，保证多副本与重试的时间语义一致（IDN-GAP-P1-04）。
	ArchiveExpiredBefore(ctx context.Context, before time.Time, limit int, archivedAt time.Time) (SessionDeleteResult, error)
	// DeleteArchivedBefore 删除 archivedAt 早于 before 的归档记录，单次最多 limit 条。
	DeleteArchivedBefore(ctx context.Context, before time.Time, limit int) (SessionDeleteResult, error)
}

// SessionExpireResult 汇总一次过期扫描的结果。
type SessionExpireResult struct {
	// Sessions 被标记为 expired 的会话数。
	Sessions int
	// Families 被标记为 expired 的设备数。
	Families int
}

// SessionDeleteResult 汇总一次保留期清理 / 归档的结果。
//
// 归档与删除共用同一结构：调用方按上下文解释为「已归档」或「已删除」的数量。
type SessionDeleteResult struct {
	// Sessions 被归档 / 删除的会话数。
	Sessions int
	// Families 被归档 / 删除的设备数。
	Families int
}

// MFAEnrollmentRepository 是 TOTP 绑定的持久化端口。
type MFAEnrollmentRepository interface {
	// FindByAccount 返回账号的绑定；不存在返回 apperr.NotFound。
	FindByAccount(ctx context.Context, accountID string) (*MFAEnrollment, error)
	// Save 创建或使用乐观锁更新账号绑定。
	//
	// expectedVersion=0 表示仅创建；>0 表示必须匹配当前版本，冲突返回 40900。
	Save(ctx context.Context, enrollment *MFAEnrollment, expectedVersion int64) error
	// ConsumeRecoveryCode 原子消费一条恢复码，返回剩余数量；已被消费返回 ok=false。
	ConsumeRecoveryCode(ctx context.Context, accountID, codeHash string, now time.Time) (remaining int, ok bool, err error)
	// Delete 删除绑定；不存在也返回成功（幂等）。
	Delete(ctx context.Context, accountID string) error
}

// OAuthIdentityRepository 是外部身份映射的持久化端口。
//
// 唯一约束为 (provider, provider_subject)：同一提供方主体只能映射一个账号。
type OAuthIdentityRepository interface {
	// FindByProviderSubject 按提供方 + 主体标识加载映射；不存在返回 apperr.NotFound。
	FindByProviderSubject(ctx context.Context, provider, subject string) (*OAuthIdentity, error)
	// Save 创建映射；唯一键冲突返回 apperr.Conflict。
	Save(ctx context.Context, identity *OAuthIdentity) error
}

// LoginAttemptRepository 是登录尝试记录的持久化端口。
//
// 只追加，不更新：失败计数与审计关联都以历史记录为准；
// CountFailuresSince 的 since 由用例按「窗口起点与最近一次成功时间的较晚者」计算。
type LoginAttemptRepository interface {
	// Append 追加一条登录尝试。
	Append(ctx context.Context, attempt *LoginAttempt) error
	// CountFailuresSince 统计 since（含）之后指定账号摘要的失败次数。
	CountFailuresSince(ctx context.Context, channel Channel, accountRef string, since time.Time) (int, error)
	// LastSuccessAt 返回最近一次成功登录时间；没有记录时 found 为 false。
	LastSuccessAt(ctx context.Context, channel Channel, accountRef string) (occurredAt time.Time, found bool, err error)
	// DeleteBefore 删除 occurred_at 早于 before 的尝试记录，单次最多 limit 条。
	DeleteBefore(ctx context.Context, before time.Time, limit int) (int, error)
}
