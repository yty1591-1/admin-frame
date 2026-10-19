// Package db 提供 database/sql 连接池装配、事务 helper 与就绪探测原语。
//
// 设计边界（对齐 DATA / SEC / REL / 架构持久化选型）：
//   - 不得依赖 internal/；驱动（mysql/postgres）由 cmd / platform 空白导入注册
//   - 不引入 ORM；仓储用 *sql.DB / *sql.Tx 或本包 Querier；sqlc 生成代码可直接挂接
//   - 禁止在 API/worker 启动路径执行 migrate（DATA-03）；本包不含迁移逻辑
//   - 错误与日志不得包含完整 DSN（SEC-05）；使用 RedactDSN
//   - 查询须带 context deadline / statement timeout（REL）；本包 Open 后 Ping 可选用超时
//
// 典型用法：
//
//	d, err := db.Open(ctx, db.Config{Driver: "postgres", DSN: dsn})
//	defer d.Close()
//	health.Register("db", d.Checker())
//	err = db.WithinTx(ctx, d.SQL(), nil, func(tx *sql.Tx) error { ... })
package db
