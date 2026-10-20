package migrate

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Dialect 指定目标数据库的 SQL 方言，用于占位符、标识符引用与咨询锁实现。
type Dialect string

const (
	// DialectPostgres 对应 PostgreSQL（占位符 $1..$n、双引号标识符、pg_advisory_lock）。
	DialectPostgres Dialect = "postgres"
	// DialectMySQL 对应 MySQL / MariaDB（? 占位符、反引号标识符、GET_LOCK）。
	DialectMySQL Dialect = "mysql"
	// DialectSQLite 对应 SQLite（? 占位符、双引号标识符；无咨询锁）。
	DialectSQLite Dialect = "sqlite"
	// DialectGeneric 对应其他 SQL 数据库（? 占位符、双引号标识符；无咨询锁）。
	DialectGeneric Dialect = "generic"
)

// String 返回方言语义名。
func (d Dialect) String() string { return string(d) }

// ParseDialect 将驱动名/配置值规范化为 Dialect。
//
// 接受 postgres/postgresql/pg、mysql/mariadb、sqlite/sqlite3、generic/其他。
// 空字符串返回错误，强制调用方显式声明方言（避免静默降级为无锁执行）。
func ParseDialect(s string) (Dialect, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "postgres", "postgresql", "pg", "pgx":
		return DialectPostgres, nil
	case "mysql", "mariadb":
		return DialectMySQL, nil
	case "sqlite", "sqlite3":
		return DialectSQLite, nil
	case "generic", "sql":
		return DialectGeneric, nil
	default:
		return "", fmt.Errorf("%w: unsupported dialect %q", ErrInvalidConfig, s)
	}
}

// Config 描述一次迁移执行所需的全部参数；零值需显式设置 Dialect 与 Migrations。
type Config struct {
	// Migrations 已加载并校验的迁移（LoadDir / LoadFS 的结果），Runner 会再校验一次。
	Migrations []Migration
	// Dialect 目标数据库方言；必填。
	Dialect Dialect
	// Table 迁移历史表名，默认 schema_migrations。
	Table string
	// Schema 历史表所在 schema（PostgreSQL 等）；为空表示使用连接默认 schema。
	Schema string

	// LockTimeout 获取咨询锁的最长等待时间；0 使用默认值 30s，负数非法。
	LockTimeout time.Duration
	// StatementTimeout 单条迁移语句超时，默认 0（不限，由 ctx 控制）。
	StatementTimeout time.Duration

	// DryRun 为 true 时只计算计划并触发 Observer：不执行迁移 SQL、不获取咨询锁，
	// 但仍会幂等创建并读取历史表，以便区分待执行迁移。
	DryRun bool
	// NoSplit 为 true 时整份 SQL 作为一条语句执行；默认按 SplitStatements 切分。
	// 仅在驱动支持多语句执行时使用。
	NoSplit bool

	// AllowOutOfOrder 允许执行版本号小于当前最大已应用版本的迁移（默认拒绝）。
	AllowOutOfOrder bool
	// AllowMissingApplied 允许历史表中存在源目录已删除的版本（默认报错）。
	AllowMissingApplied bool

	// Observer 接收执行事件（可为 nil）；回调在调用方 goroutine 同步执行。
	Observer Observer
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ApplyDefaults 填充 Table 与 LockTimeout 的默认值；不修改其他字段。
func (c *Config) ApplyDefaults() {
	if c.Table == "" {
		c.Table = "schema_migrations"
	}
	if c.LockTimeout == 0 {
		c.LockTimeout = 30 * time.Second
	}
}

// Validate 校验配置与迁移；不修改接收者。
//
// 注意：Validate 会拒绝空 Dialect、非法标识符、负超时、重复/未排序的迁移版本。
func (c Config) Validate() error {
	if _, err := ParseDialect(string(c.Dialect)); err != nil {
		return err
	}
	if !identifierPattern.MatchString(c.Table) {
		return fmt.Errorf("%w: invalid table name %q", ErrInvalidConfig, c.Table)
	}
	if c.Schema != "" && !identifierPattern.MatchString(c.Schema) {
		return fmt.Errorf("%w: invalid schema name %q", ErrInvalidConfig, c.Schema)
	}
	if c.LockTimeout < 0 {
		return fmt.Errorf("%w: LockTimeout must be >= 0", ErrInvalidConfig)
	}
	if c.StatementTimeout < 0 {
		return fmt.Errorf("%w: StatementTimeout must be >= 0", ErrInvalidConfig)
	}
	return ValidateMigrations(c.Migrations)
}

// quoteIdent 按方言引用标识符；调用前须通过 Config.Validate 校验名称。
func (d Dialect) quoteIdent(name string) string {
	if d == DialectMySQL {
		return "`" + name + "`"
	}
	return `"` + name + `"`
}

// placeholder 返回第 n 个（从 1 开始）绑定参数占位符。
func (d Dialect) placeholder(n int) string {
	if d == DialectPostgres {
		return "$" + strconv.Itoa(n)
	}
	return "?"
}

// placeholders 返回从 1 到 n 的占位符列表。
func (d Dialect) placeholders(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = d.placeholder(i + 1)
	}
	return out
}
