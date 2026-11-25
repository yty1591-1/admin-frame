package domain

import (
	"context"
	"time"
)

// SecretRepository 是业务密钥与版本的持久化端口。
//
// 列表方法不得返回密文列；GetVersion 仅供解密路径使用。
type SecretRepository interface {
	Create(ctx context.Context, secret Secret, version SecretVersion) error
	GetByID(ctx context.Context, id int64) (Secret, error)
	GetByName(ctx context.Context, env, namespace, name string) (Secret, error)
	List(ctx context.Context, env, namespace string, status SecretStatus, limit, offset int) ([]Secret, int, error)
	UpdateCAS(ctx context.Context, secret Secret, expectedVersion int64) error
	AppendVersion(ctx context.Context, version SecretVersion) error
	GetVersion(ctx context.Context, secretID, version int64) (SecretVersion, error)
	ListVersions(ctx context.Context, secretID int64, limit, offset int) ([]SecretVersion, int, error)
	UpdateVersionStatus(ctx context.Context, secretID, version int64, from, to SecretVersionStatus, at time.Time) error
	AppendReview(ctx context.Context, review SecretReview) error
	ListReviews(ctx context.Context, secretID int64) ([]SecretReview, error)
}

// SecretGrantRepository 是服务授权持久化端口。
type SecretGrantRepository interface {
	Create(ctx context.Context, grant SecretGrant) error
	GetByID(ctx context.Context, id int64) (SecretGrant, error)
	ListBySecret(ctx context.Context, secretID int64) ([]SecretGrant, error)
	FindActive(ctx context.Context, env, namespace, serviceIdentity string, secretID int64, now time.Time) (SecretGrant, error)
	// Activate 将 pending 授权升级为 resolve（双人审批通过）。
	Activate(ctx context.Context, id int64) error
	Revoke(ctx context.Context, id int64, at time.Time) error
}

// SecretAccessRepository 可选：解析访问投影；MVP 可用内存环形缓冲。
type SecretAccessRepository interface {
	Append(ctx context.Context, access SecretAccess) error
	ListBySecretName(ctx context.Context, env, namespace, name string, limit, offset int) ([]SecretAccess, int, error)
}
