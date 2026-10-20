package migrate

import stderrors "errors"

var (
	// ErrInvalidConfig 表示 Runner 配置或迁移源参数不合法（错误可用 errors.Is 判定）。
	ErrInvalidConfig = stderrors.New("migrate: invalid config")
	// ErrInvalidSource 表示迁移目录、文件名或文件内容不符合 DATA-01/02。
	ErrInvalidSource = stderrors.New("migrate: invalid source")
	// ErrMalformedSQL 表示 SQL 脚本无法切分（引号、注释或 dollar-quote 未闭合）。
	ErrMalformedSQL = stderrors.New("migrate: malformed sql")
	// ErrChecksumMismatch 表示已应用迁移的 checksum 与当前文件不一致（历史漂移）。
	ErrChecksumMismatch = stderrors.New("migrate: checksum mismatch")
	// ErrDirty 表示历史表存在未完成（dirty）的迁移，须人工确认数据库状态后清理。
	ErrDirty = stderrors.New("migrate: dirty migration")
	// ErrOutOfOrder 表示存在版本号小于当前已应用版本的待执行迁移。
	ErrOutOfOrder = stderrors.New("migrate: out-of-order migration")
	// ErrMissingMigration 表示历史表记录的版本在源目录中不存在。
	ErrMissingMigration = stderrors.New("migrate: applied migration missing from source")
	// ErrMissingDown 表示迁移缺少 down SQL（DATA-02）。
	ErrMissingDown = stderrors.New("migrate: missing down migration")
	// ErrLockTimeout 表示获取迁移咨询锁失败或超时。
	ErrLockTimeout = stderrors.New("migrate: lock timeout")
	// ErrStatementFailed 表示迁移语句执行失败；可用 errors.Unwrap 取数据库原始错误。
	ErrStatementFailed = stderrors.New("migrate: statement failed")
	// ErrCommitFailed 表示迁移事务提交失败；数据库状态可能不确定，须人工确认。
	ErrCommitFailed = stderrors.New("migrate: commit failed")
	// ErrNotApplied 表示尝试回滚的版本未应用。
	ErrNotApplied = stderrors.New("migrate: migration not applied")
)
