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
//   - channel / subject / merchant_id 创建后不可变，仓储须拒绝变更；
//   - 读方法返回副本，调用方修改返回值不得影响仓储内部状态。
type AccountRepository interface {
	// FindByID 按主键加载账号。
	FindByID(ctx context.Context, id string) (*Account, error)
	// FindBySubject 按端别 + 规范化主体加载账号；软删账号仍返回，用于阻止 subject 复用。
	FindBySubject(ctx context.Context, channel Channel, subject string) (*Account, error)
	// Save 创建或更新账号；expectedVersion 为 0 表示创建。
	Save(ctx context.Context, account *Account, expectedVersion int64) error
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
//   - RevokeFamily 在同一语句内把整条会话链置为 revoked（含已 rotated 的会话）。
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
	// ListByAccount 返回账号全部会话，按创建时间倒序，供会话管理使用。
	ListByAccount(ctx context.Context, accountID string) ([]*Session, error)
}

// MFAEnrollmentRepository 是 TOTP 绑定的持久化端口。
type MFAEnrollmentRepository interface {
	// FindByAccount 返回账号的绑定；不存在返回 apperr.NotFound。
	FindByAccount(ctx context.Context, accountID string) (*MFAEnrollment, error)
	// Save 创建或覆盖账号绑定（account_id 为主键）。
	Save(ctx context.Context, enrollment *MFAEnrollment) error
	// Delete 删除绑定；不存在也返回成功（幂等）。
	Delete(ctx context.Context, accountID string) error
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
}
