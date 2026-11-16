// Command configcenter-drill 在本机 MySQL 上执行配置中心拉取模型演练取证（ADR-0016）。
//
// 覆盖：首次播种 → 改热更键 → 双人审批发布 → SnapshotService 拉取 → 回滚 →
// 本地 DirCache 落盘与完整性校验。
//
// 用法：
//
//	$env:ADMIN_FRAME_DB_DSN='root:root@tcp(127.0.0.1:3306)/admin_frame_dev?parseTime=true&loc=UTC&charset=utf8mb4'
//	go run ./scripts/local/configcenter-drill -out docs/incident/drills/artifacts/configcenter-pull
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/configclient"
	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/id"
)

func main() {
	outDir := flag.String("out", "docs/incident/drills/artifacts/configcenter-pull", "取证输出目录")
	envName := flag.String("env", "dev", "配置环境（local/dev/test/staging/prod）")
	namespace := flag.String("namespace", "platform", "命名空间")
	flag.Parse()

	dsn := os.Getenv("ADMIN_FRAME_DB_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "ADMIN_FRAME_DB_DSN is required")
		os.Exit(1)
	}
	if err := run(*outDir, *envName, *namespace, dsn); err != nil {
		fmt.Fprintf(os.Stderr, "configcenter-drill: %v\n", err)
		os.Exit(1)
	}
}

type evidence struct {
	StartedAt           string `json:"started_at"`
	FinishedAt          string `json:"finished_at"`
	Env                 string `json:"env"`
	Namespace           string `json:"namespace"`
	BootstrapReleaseID  int64  `json:"bootstrap_release_id"`
	BootstrapItemCount  int    `json:"bootstrap_item_count"`
	UpdatedKey          string `json:"updated_key"`
	UpdatedValue        string `json:"updated_value"`
	PublishedReleaseID  int64  `json:"published_release_id"`
	RollbackReleaseID   int64  `json:"rollback_release_id"`
	ActiveAfterRollback int64  `json:"active_after_rollback"`
	PulledValue         string `json:"pulled_value"`
	CacheDir            string `json:"cache_dir"`
	CacheVerified       bool   `json:"cache_verified"`
	Result              string `json:"result"`
}

func run(outDir, envName, namespace, dsn string) error {
	started := time.Now().UTC()
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return err
	}

	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("open mysql: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping mysql: %w", err)
	}

	repos, err := persistence.New(sqlDB)
	if err != nil {
		return err
	}
	registry, err := spec.Platform()
	if err != nil {
		return err
	}
	ids := &seqIDs{next: time.Now().UnixNano()%1_000_000_000 + 1_000_000_000}
	emitter := quietEmitter{}
	opts := app.Options{Service: "configcenter-drill"}

	bootstrap, err := app.NewBootstrapService(repos.Items, repos.Releases, repos.Pointers, registry, emitter, ids, opts)
	if err != nil {
		return err
	}
	items, err := app.NewItemService(repos.Items, registry, emitter, ids, opts)
	if err != nil {
		return err
	}
	releases, err := app.NewReleaseService(repos.Items, repos.Releases, repos.Pointers, emitter, ids, opts)
	if err != nil {
		return err
	}
	snapshots, err := app.NewSnapshotService(repos.Items, repos.Releases, repos.Pointers)
	if err != nil {
		return err
	}

	writer := app.Actor{SubjectID: "drill-writer", ActorType: "user", Channel: "admin", TraceID: "drill-1"}
	reviewer := app.Actor{SubjectID: "drill-reviewer", ActorType: "user", Channel: "admin", TraceID: "drill-2"}

	var seedResult app.BootstrapResult
	if err := db.WithinUnitOfWork(ctx, sqlDB, nil, func(txCtx context.Context) error {
		var berr error
		seedResult, berr = bootstrap.EnsureBaseline(txCtx, app.BootstrapInput{
			Env: envName, Namespace: namespace, Actor: writer,
		})
		return berr
	}); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}

	const updateKey = "log.level"
	const updateValue = "debug"
	item, err := repos.Items.GetByKey(ctx, envName, namespace, updateKey)
	if err != nil {
		return fmt.Errorf("get item %s: %w", updateKey, err)
	}
	ver := item.Version
	if _, err := items.UpdateValue(ctx, app.UpdateValueInput{
		Env: envName, Namespace: namespace, Key: updateKey,
		Value: updateValue, ChangeSummary: "drill", ExpectedVersion: &ver, Actor: writer,
	}); err != nil {
		return fmt.Errorf("update value: %w", err)
	}
	item, err = repos.Items.GetByKey(ctx, envName, namespace, updateKey)
	if err != nil {
		return err
	}

	var published domain.Release
	if err := db.WithinUnitOfWork(ctx, sqlDB, nil, func(txCtx context.Context) error {
		release, cerr := releases.CreateRelease(txCtx, app.CreateReleaseInput{
			Env: envName, Namespace: namespace, Name: "drill-publish",
			Strategy: domain.StrategyAll,
			Entries:  []app.SnapshotEntry{{ItemID: item.ID, ItemVersion: item.CurrentVersion}},
			Actor:    writer,
		})
		if cerr != nil {
			return cerr
		}
		if _, cerr = releases.Submit(txCtx, release.ID, writer); cerr != nil {
			return cerr
		}
		if _, cerr = releases.Approve(txCtx, app.ApproveInput{
			ReleaseID: release.ID, Decision: domain.DecisionApproved,
			Comment: "drill", Actor: reviewer,
		}); cerr != nil {
			return cerr
		}
		published, cerr = releases.Publish(txCtx, release.ID, nil, writer)
		return cerr
	}); err != nil {
		return fmt.Errorf("publish: %w", err)
	}

	pulled, err := snapshots.Load(ctx, envName, namespace)
	if err != nil {
		return fmt.Errorf("pull snapshot: %w", err)
	}
	if err := pulled.ValidateIntegrity(); err != nil {
		return fmt.Errorf("validate pulled: %w", err)
	}

	var rollback domain.Release
	if err := db.WithinUnitOfWork(ctx, sqlDB, nil, func(txCtx context.Context) error {
		var rerr error
		rollback, rerr = releases.Rollback(txCtx, app.RollbackInput{
			TargetReleaseID: seedResult.ReleaseID,
			Name:            "drill-rollback",
			Actor:           writer,
		})
		return rerr
	}); err != nil {
		return fmt.Errorf("rollback: %w", err)
	}
	activeID := rollback.ID
	if ptr, perr := repos.Pointers.Get(ctx, envName, namespace); perr == nil {
		activeID = ptr.ActiveReleaseID
	}

	cacheDir := filepath.Join(outDir, "cache")
	cache, err := configclient.NewDirCache(cacheDir)
	if err != nil {
		return err
	}
	if err := cache.SaveNamespace(ctx, pulled); err != nil {
		return fmt.Errorf("save cache: %w", err)
	}
	loaded, err := cache.LoadNamespace(ctx, namespace)
	if err != nil {
		return fmt.Errorf("load cache: %w", err)
	}
	cacheOK := loaded.ValidateIntegrity() == nil && loaded.Values[updateKey] == updateValue

	ev := evidence{
		StartedAt: started.Format(time.RFC3339), FinishedAt: time.Now().UTC().Format(time.RFC3339),
		Env: envName, Namespace: namespace,
		BootstrapReleaseID: seedResult.ReleaseID, BootstrapItemCount: seedResult.ItemCount,
		UpdatedKey: updateKey, UpdatedValue: updateValue,
		PublishedReleaseID: published.ID, RollbackReleaseID: rollback.ID,
		ActiveAfterRollback: activeID, PulledValue: pulled.Values[updateKey],
		CacheDir: cacheDir, CacheVerified: cacheOK, Result: "pass",
	}
	if !cacheOK {
		ev.Result = "fail"
	}
	raw, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return err
	}
	outFile := filepath.Join(outDir, "evidence.json")
	if err := os.WriteFile(outFile, raw, 0o640); err != nil {
		return err
	}
	fmt.Printf("configcenter-drill %s → %s\n", ev.Result, outFile)
	if ev.Result != "pass" {
		return fmt.Errorf("drill failed")
	}
	return nil
}

type quietEmitter struct{}

func (quietEmitter) Emit(context.Context, audit.Event) error { return nil }

type seqIDs struct{ next int64 }

func (s *seqIDs) Next() (int64, error) {
	return atomic.AddInt64(&s.next, 1), nil
}

func (s *seqIDs) NextString() (string, error) {
	n, err := s.Next()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", n), nil
}

var _ id.Generator = (*seqIDs)(nil)
