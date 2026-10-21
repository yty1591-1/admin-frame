package cache

import "context"

// SubjectInvalidator 把 SnapshotCache 适配为 app 侧的快照失效端口。
//
// env 在装配期固定（同一进程只服务一个环境），避免调用方到处传环境参数；
// 失效失败返回错误，由 app 记录指标后继续——L1 3s / L2 5m 的 TTL 是最终兜底。
type SubjectInvalidator struct {
	cache *SnapshotCache
	env   string
}

// NewSubjectInvalidator 构造失效适配器。
func NewSubjectInvalidator(c *SnapshotCache, env string) *SubjectInvalidator {
	return &SubjectInvalidator{cache: c, env: env}
}

// InvalidateSubject 失效单个主体的快照。
func (i *SubjectInvalidator) InvalidateSubject(ctx context.Context, channel, merchantID, subjectType, subjectID string) error {
	if i == nil || i.cache == nil {
		return ErrInvalidConfig
	}
	return i.cache.DeleteSubject(ctx, i.env, channel, merchantID, subjectType, subjectID)
}
