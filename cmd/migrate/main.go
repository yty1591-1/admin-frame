// Package main 提供 schema 迁移 CLI（up/down/status/version），是 DATA-03 规定的唯一执行入口。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/migrate"
)

// 由 -ldflags -X 注入（RELS-03）；本地默认 dev。
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

func main() {
	configPath := flag.String("config", "", "optional config file; reads db.driver / db.dsn")
	dir := flag.String("dir", "migrations", "migrations directory")
	action := flag.String("action", "up", "up|down|status|version")
	driver := flag.String("driver", "", "database driver: postgres|pgx|mysql (overrides config/env)")
	dsn := flag.String("dsn", "", "database DSN (overrides config/env; prefer ADMIN_FRAME_DB_DSN)")
	steps := flag.Int("steps", 1, "down: number of migrations to roll back")
	to := flag.Int64("to", -1, "up: stop after this version (inclusive); down: target version to keep (>=0 overrides -steps)")
	table := flag.String("table", "schema_migrations", "migration history table")
	schema := flag.String("schema", "", "schema of the history table (PostgreSQL)")
	dryRun := flag.Bool("dry-run", false, "print planned migrations without executing them")
	noSplit := flag.Bool("no-split", false, "execute each SQL file as one statement")
	allowOutOfOrder := flag.Bool("allow-out-of-order", false, "allow pending versions lower than the current version")
	lockTimeout := flag.Duration("lock-timeout", 30*time.Second, "advisory lock acquisition timeout")
	statementTimeout := flag.Duration("statement-timeout", 0, "per-statement timeout (0 = unlimited)")
	timeout := flag.Duration("timeout", 30*time.Minute, "overall command timeout (0 = unlimited)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("migrate %s (commit %s, built %s, go %s)\n", version, commit, buildTime, runtimeVersion())
		return
	}

	opts := options{
		ConfigPath:       *configPath,
		Dir:              *dir,
		Action:           *action,
		Driver:           *driver,
		DSN:              *dsn,
		Steps:            *steps,
		To:               *to,
		Table:            *table,
		Schema:           *schema,
		DryRun:           *dryRun,
		NoSplit:          *noSplit,
		AllowOutOfOrder:  *allowOutOfOrder,
		LockTimeout:      *lockTimeout,
		StatementTimeout: *statementTimeout,
	}
	if err := runWithSignal(opts, *timeout); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
}

// runWithSignal 装配信号与总超时上下文；defer 收敛在此，避免 main 中 Fatalf 跳过清理。
func runWithSignal(opts options, timeout time.Duration) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return run(ctx, opts)
}

// options 汇总 CLI 参数，便于 run 独立测试与阅读。
type options struct {
	ConfigPath       string
	Dir              string
	Action           string
	Driver           string
	DSN              string
	Steps            int
	To               int64
	Table            string
	Schema           string
	DryRun           bool
	NoSplit          bool
	AllowOutOfOrder  bool
	LockTimeout      time.Duration
	StatementTimeout time.Duration
}

func run(ctx context.Context, opts options) error {
	dbCfg, err := platformconfig.LoadDBConfig(opts.ConfigPath)
	if err != nil {
		return err
	}
	if opts.Driver != "" {
		dbCfg.Driver = opts.Driver
	}
	if opts.DSN != "" {
		dbCfg.DSN = opts.DSN
	}
	if dbCfg.Driver == "" {
		return fmt.Errorf("db driver is required: set ADMIN_FRAME_DB_DRIVER, -driver or db.driver")
	}
	if strings.EqualFold(dbCfg.Driver, "memory") {
		return fmt.Errorf("db driver %q is not a database/sql driver", dbCfg.Driver)
	}
	if dbCfg.DSN == "" {
		return fmt.Errorf("db dsn is required: set ADMIN_FRAME_DB_DSN, -dsn or db.dsn")
	}
	dialect, err := migrate.ParseDialect(dbCfg.Driver)
	if err != nil {
		return err
	}

	var openDriver string
	switch dialect {
	case migrate.DialectPostgres:
		// pgx stdlib 注册的驱动名为 pgx；同时接受 postgres / postgresql 别名。
		openDriver = "pgx"
	case migrate.DialectMySQL:
		// mariadb 等别名统一走已注册的 mysql 驱动。
		openDriver = "mysql"
		// 迁移脚本依赖连接 collation 与表级 DDL（utf8mb4_0900_ai_ci）一致，
		// 显式配置成其他值会令种子迁移的用户变量比较报 1267，这里强制对齐。
		dsn, err := db.ForceMySQLCollation(openDriver, dbCfg.DSN, db.MySQLDefaultCollation)
		if err != nil {
			return fmt.Errorf("migrate: normalize mysql dsn: %w", err)
		}
		dbCfg.DSN = dsn
	default:
		return fmt.Errorf("db driver %q is not supported by this binary (registered: postgres, mysql)", dbCfg.Driver)
	}
	sqlDB, err := db.Open(ctx, db.Config{
		Driver:       openDriver,
		DSN:          dbCfg.DSN,
		MaxOpenConns: 4,
		MaxIdleConns: 2,
		PingTimeout:  5 * time.Second,
	})
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			log.Printf("migrate: close db: %v", closeErr)
		}
	}()

	migrations, err := migrate.LoadDir(opts.Dir, migrate.LoadOptions{})
	if err != nil {
		return err
	}
	runner, err := migrate.New(sqlDB.SQL(), migrate.Config{
		Migrations:       migrations,
		Dialect:          dialect,
		Table:            opts.Table,
		Schema:           opts.Schema,
		LockTimeout:      opts.LockTimeout,
		StatementTimeout: opts.StatementTimeout,
		DryRun:           opts.DryRun,
		NoSplit:          opts.NoSplit,
		AllowOutOfOrder:  opts.AllowOutOfOrder,
		Observer:         migrate.TextReporter(os.Stdout),
	})
	if err != nil {
		return err
	}

	switch opts.Action {
	case "up":
		var (
			res migrate.Result
			err error
		)
		if opts.To >= 0 {
			res, err = runner.UpTo(ctx, opts.To)
		} else {
			res, err = runner.Up(ctx)
		}
		if err != nil {
			return err
		}
		printResult(res)
		return nil
	case "down":
		var (
			res migrate.Result
			err error
		)
		if opts.To >= 0 {
			res, err = runner.DownTo(ctx, opts.To)
		} else {
			res, err = runner.DownSteps(ctx, opts.Steps)
		}
		if err != nil {
			return err
		}
		printResult(res)
		return nil
	case "status":
		st, err := runner.Status(ctx)
		if err != nil {
			return err
		}
		printStatus(os.Stdout, st)
		return nil
	case "version":
		st, err := runner.Status(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("%06d\n", st.Current)
		return nil
	default:
		return fmt.Errorf("unknown action %q (want up|down|status|version)", opts.Action)
	}
}

func printResult(res migrate.Result) {
	if len(res.Steps) == 0 {
		fmt.Println("no migrations to execute")
		return
	}
	prefix := ""
	if res.DryRun {
		prefix = "planned "
	}
	fmt.Printf("%s%s: %d migration(s)\n", prefix, res.Direction, len(res.Steps))
}
