// Package main 提供身份域 PII 回填工具（IDN-GAP-P1-06 S3）。
//
// 用途：为存量 identity_account 与 identity_oauth_identity 回填确定性索引与密文。
// 幂等：只处理缺失保护列的行，中断后可重复执行；不修改明文列，可安全回滚。
// 前置：已执行 migrations/000011，且尚未执行 000015（明文列仍存在）。
// 适用：升级到 S5（删除明文列）之前必须完成本回填。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/pkg/crypto"
	"github.com/admin-frame/admin-frame/pkg/db"
)

func main() {
	configPath := flag.String("config", "", "config file; reads db and identity.subject / identity.hash_key")
	batch := flag.Int("batch", 500, "rows per batch")
	maxBatches := flag.Int("max-batches", 1000, "max batches per run (0 = unlimited)")
	timeout := flag.Duration("timeout", 30*time.Minute, "overall command timeout (0 = unlimited)")
	flag.Parse()

	if err := run(*configPath, *batch, *maxBatches, *timeout); err != nil {
		fmt.Fprintf(os.Stderr, "backfill-subject: %v\n", err)
		os.Exit(1)
	}
}

// run 加载配置、构造保护器并分批回填。
func run(configPath string, batch, maxBatches int, timeout time.Duration) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cfg, err := platform.LoadConfig(configPath)
	if err != nil {
		return err
	}
	if cfg.DB.Driver == "" || cfg.DB.Driver == "memory" {
		return fmt.Errorf("db.driver must be a real database driver, got %q", cfg.DB.Driver)
	}
	if cfg.DB.DSN == "" {
		return fmt.Errorf("db.dsn is required")
	}

	openDriver := cfg.DB.Driver
	switch openDriver {
	case "postgres", "postgresql", "pgx":
		// pgx stdlib 注册的驱动名为 pgx；兼容配置中的 postgres 别名。
		openDriver = "pgx"
	case "mysql", "mariadb":
		openDriver = "mysql"
	}

	sqlDB, err := db.Open(ctx, db.Config{
		Driver:       openDriver,
		DSN:          cfg.DB.DSN,
		MaxOpenConns: 4,
		MaxIdleConns: 2,
		PingTimeout:  5 * time.Second,
	})
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			log.Printf("backfill-subject: close db: %v", closeErr)
		}
	}()

	protector, err := buildProtector(cfg)
	if err != nil {
		return err
	}
	repository := persistence.NewSQLAccountRepository(sqlDB.SQL(), persistence.WithSubjectProtector(protector))
	oauthRepository := persistence.NewSQLOAuthIdentityRepository(sqlDB.SQL(),
		persistence.WithOAuthSubjectProtector(protector))

	total := 0
	for round := 0; maxBatches <= 0 || round < maxBatches; round++ {
		updated, backfillErr := repository.BackfillSubjectProtection(ctx, batch)
		if backfillErr != nil {
			return backfillErr
		}
		total += updated
		fmt.Printf("account batch %d: updated %d rows (total %d)\n", round+1, updated, total)
		if updated < batch {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	oauthTotal := 0
	for round := 0; maxBatches <= 0 || round < maxBatches; round++ {
		updated, backfillErr := oauthRepository.BackfillProviderProtection(ctx, batch)
		if backfillErr != nil {
			return backfillErr
		}
		oauthTotal += updated
		fmt.Printf("oauth batch %d: updated %d rows (total %d)\n", round+1, updated, oauthTotal)
		if updated < batch {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	fmt.Printf("backfill-subject completed: accounts=%d oauth=%d\n", total, oauthTotal)
	return nil
}

// buildProtector 复用运行时的密钥约定：索引用 hash_key，密文用 subject.keys。
func buildProtector(cfg *platform.Config) (persistence.SubjectProtector, error) {
	active, keys, err := cfg.Identity.DecodeHashKeys()
	if err != nil {
		return nil, fmt.Errorf("identity hash keys: %w", err)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("identity.hash_key is required for subject_index")
	}
	hasher, err := crypto.NewHasher(active, keys)
	if err != nil {
		return nil, err
	}
	subjectKeys, err := cfg.Identity.Subject.DecodeKeys()
	if err != nil {
		return nil, err
	}
	if len(subjectKeys) == 0 {
		return nil, fmt.Errorf("identity.subject.keys is required for subject_ciphertext")
	}
	legacy, err := cfg.Identity.Subject.DecodeLegacyKey()
	if err != nil {
		return nil, err
	}
	ring, err := crypto.NewKeyRing(subjectKeys, cfg.Identity.Subject.ActiveKeyID, legacy)
	if err != nil {
		return nil, err
	}
	return persistence.NewSubjectProtector(hasher, ring), nil
}
