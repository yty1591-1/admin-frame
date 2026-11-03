package db

import (
	"database/sql"
	"net/url"
	"strings"

	"github.com/XSAM/otelsql"
	"github.com/go-sql-driver/mysql"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// span 属性键直接写字面量：只需要 db.system / db.name 两个键，
// 不值得为此把 semconv 包版本锁进 pkg/db（semconv 升版会改键名）。
const (
	attrDBSystem = attribute.Key("db.system")
	attrDBName   = attribute.Key("db.name")
)

// dbSystem 把 database/sql 驱动名映射为 OTel 语义约定的 db.system 取值。
func dbSystem(driver string) string {
	switch driver {
	case "mysql", "mariadb":
		return "mysql"
	case "pgx", "postgres", "postgresql":
		return "postgresql"
	default:
		return driver
	}
}

// databaseName 从 DSN 解析目标库名，仅用于 span 属性。
//
// 解析失败返回空串：库名只影响观测可读性，不能因此让建连失败。
func databaseName(driver, dsn string) string {
	switch driver {
	case "mysql", "mariadb":
		cfg, err := mysql.ParseDSN(dsn)
		if err != nil {
			return ""
		}
		return cfg.DBName
	case "pgx", "postgres", "postgresql":
		return postgresDatabaseName(dsn)
	default:
		return ""
	}
}

// postgresDatabaseName 支持 URL 与 key=value 两种 pgx DSN 形式。
func postgresDatabaseName(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			return ""
		}
		return strings.TrimPrefix(parsed.Path, "/")
	}
	for _, field := range strings.Fields(dsn) {
		if name, ok := strings.CutPrefix(field, "dbname="); ok {
			return name
		}
	}
	return ""
}

// openSQL 按配置打开连接池；tp 为 nil 时直接用 database/sql，不包装驱动。
//
// tp 非 nil 时用 otelsql 包装驱动，为 QueryContext / ExecContext / BeginTx /
// Commit 等调用创建 CLIENT span，父子关系沿用调用方 context ——
// 「HTTP 入口 span → SQL span」的链路即由此建立（AC-ENT-06-4）。
//
// 用 otelsql 而不是自研 driver 包装：包装器必须精确转发 CheckNamedValue /
// ResetSession / IsValid / ColumnConverter 等可选接口，漏一个就会静默改变驱动
// 行为（pgx 依赖 CheckNamedValue 直通原生类型），这类错误只在真库上才暴露。
func openSQL(driver, dsn string, tp trace.TracerProvider) (*sql.DB, error) {
	if tp == nil {
		return sql.Open(driver, dsn)
	}
	opts := []otelsql.Option{
		otelsql.WithTracerProvider(tp),
		otelsql.WithAttributes(attrDBSystem.String(dbSystem(driver))),
	}
	if name := databaseName(driver, dsn); name != "" {
		opts = append(opts, otelsql.WithAttributes(attrDBName.String(name)))
	}
	return otelsql.Open(driver, dsn, opts...)
}
