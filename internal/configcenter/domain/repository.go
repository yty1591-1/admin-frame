package domain

import (
	"context"
	"time"
)

// ItemRepository 是配置键与其版本的持久化端口。
//
// 写方法遵循 optimistic locking：UpdateCAS 的 expectedVersion 不匹配返回
// ErrVersionConflict；Create / AppendVersion 的唯一键冲突返回 ErrDuplicateKey。
// Create 与 AppendVersion 需要原子生效时，由调用方在同一 db.WithinUnitOfWork 内执行。
type ItemRepository interface {
	Create(ctx context.Context, item Item) error
	GetByID(ctx context.Context, id int64) (Item, error)
	GetByKey(ctx context.Context, env, namespace, key string) (Item, error)
	List(ctx context.Context, env, namespace string, status ItemStatus, limit, offset int) ([]Item, int, error)
	UpdateCAS(ctx context.Context, item Item, expectedVersion int64) error
	AppendVersion(ctx context.Context, version ItemVersion) error
	GetVersion(ctx context.Context, itemID, version int64) (ItemVersion, error)
	ListVersions(ctx context.Context, itemID int64, limit, offset int) ([]ItemVersion, int, error)
}

// ReleaseRepository 是发布快照、快照条目与审批记录的持久化端口。
//
// Create 必须在同一事务内写入 release 头与全部条目；NextReleaseNo 只提供
// 候选值，唯一键 uk_config_release_no 是并发下的最终防线。
type ReleaseRepository interface {
	Create(ctx context.Context, release Release, items []ReleaseItem) error
	Get(ctx context.Context, id int64) (Release, error)
	GetByNo(ctx context.Context, env, namespace string, releaseNo int64) (Release, error)
	List(ctx context.Context, env, namespace string, status ReleaseStatus, limit, offset int) ([]Release, int, error)
	NextReleaseNo(ctx context.Context, env, namespace string) (int64, error)
	Transition(ctx context.Context, id int64, from, to ReleaseStatus, actor string, at time.Time) error
	// UpdateSnapshotSignature 在发布生效前写入 Ed25519 签名与 key id。
	// 快照内容不可变，签名只覆盖既有 snapshot_hash / snapshot_size 等元数据。
	UpdateSnapshotSignature(ctx context.Context, id int64, signature, keyID string, at time.Time) error
	ListItems(ctx context.Context, releaseID int64) ([]ReleaseItem, error)
	AppendReview(ctx context.Context, review ReleaseReview) error
	ListReviews(ctx context.Context, releaseID int64) ([]ReleaseReview, error)
}

// PointerRepository 是生效发布指针的持久化端口。
//
// CompareAndSwap 以 expectedVersion 做 CAS：成功返回新指针（version+1），
// 版本不匹配返回 ErrPointerConflict，指针不存在返回 ErrNotFound。
// Create 只在 (env, namespace) 首次初始化时调用，冲突返回 ErrDuplicateKey。
type PointerRepository interface {
	Get(ctx context.Context, env, namespace string) (Pointer, error)
	Create(ctx context.Context, pointer Pointer) error
	CompareAndSwap(ctx context.Context, env, namespace string, expectedVersion, activeReleaseID int64, now time.Time) (Pointer, error)
}
