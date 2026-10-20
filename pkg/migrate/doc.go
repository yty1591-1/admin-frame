// Package migrate 提供数据库 schema 迁移的生产级执行引擎。
//
// 本包按 DATA_STANDARDS 加载 migrations/<NNNNNN>_<desc>.up.sql 与配对 .down.sql，
// 校验命名与配对、计算 up SQL 的 SHA-256、维护迁移历史表，并在咨询锁保护下串行执行。
//
// 职责边界：
//   - 仅依赖 database/sql 与标准库；不导入 internal/，不绑定具体驱动
//     （驱动由 cmd/migrate 空白导入注册）。
//   - 不在业务 API / worker 启动路径使用（DATA-03）；唯一入口为 cmd/migrate 或发布流水线。
//   - 不做 schema 差异推断（不是 ORM AutoMigrate），只执行显式 SQL 文件。
//
// 关键保证：
//   - 咨询锁：PostgreSQL 使用 pg_advisory_lock，MySQL 使用 GET_LOCK，保证多实例串行；
//     锁占用一条独占连接，因此连接池上限必须 >= 2（New 会拒绝 MaxOpenConns=1）。
//     其他方言降级为无锁，须由部署侧保证同一时刻只有一个执行者。
//   - 每个迁移默认在事务内执行，并写入 dirty 标记；执行成功后清除。MySQL 等 DDL
//     隐式提交的数据库失败时会留下 dirty 记录，阻断后续执行并提示人工确认。
//     可用 SQL 指令 "-- +migrate no-transaction" 显式声明非事务迁移（如
//     CREATE INDEX CONCURRENTLY）。
//   - 漂移检测：历史表中的 checksum 与当前文件不一致时拒绝执行，避免已应用迁移被静默改写。
//   - 顺序检测：默认拒绝乱序补丁；已应用但源文件缺失时默认报错。
//
// 典型用法：
//
//	migs, err := migrate.LoadDir("migrations", migrate.LoadOptions{})
//	if err != nil {
//		return err
//	}
//	r, err := migrate.New(sqlDB, migrate.Config{
//		Migrations: migs,
//		Dialect:    migrate.DialectPostgres,
//		Table:      "schema_migrations",
//		Observer:   migrate.TextReporter(os.Stdout),
//	})
//	if err != nil {
//		return err
//	}
//	if _, err := r.Up(ctx); err != nil {
//		return err
//	}
package migrate
