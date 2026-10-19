package contextx

// 未导出 key，避免与其它包冲突（架构 S-04）。
type (
	userKey     struct{}
	merchantKey struct{}
	channelKey  struct{}
	rolesKey    struct{}
)
