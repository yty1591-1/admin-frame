package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Step 描述一次成功执行（或 dry-run 计划）的迁移。
type Step struct {
	// Version 迁移版本号。
	Version int64
	// Description 迁移描述。
	Description string
	// Direction 执行方向。
	Direction Direction
	// Statements 实际执行的语句数（NoSplit 时为 1）。
	Statements int
	// Duration 执行耗时；dry-run 时为 0。
	Duration time.Duration
}

// Result 是一次 Up / Down 调用的汇总。
type Result struct {
	// Direction 本次执行方向。
	Direction Direction
	// DryRun 表示未执行迁移 SQL。
	DryRun bool
	// Steps 按执行顺序排列的迁移步骤。
	Steps []Step
}

// Status 是数据库当前迁移状态快照。
type Status struct {
	// Current 已应用的最大版本号（0 表示无记录）。
	Current int64
	// Dirty 表示存在未完成的迁移，须人工确认。
	Dirty bool
	// Applied 历史表记录（按版本升序）。
	Applied []Record
	// Pending 尚未应用的迁移（按版本升序）。
	Pending []Migration
	// Drift 已应用但 checksum 与当前文件不一致的版本。
	Drift []int64
	// Missing 已应用但源目录中不存在的版本。
	Missing []int64
}

// Runner 执行迁移；创建后只读，可并发调用 Status，但 Up/Down 应串行（内部有咨询锁）。
type Runner struct {
	db      *sql.DB
	cfg     Config
	dialect Dialect
}

// New 校验配置并构造 Runner。
//
// 返回错误时保证可用 errors.Is 匹配 ErrInvalidConfig / ErrInvalidSource。
func New(db *sql.DB, cfg Config) (*Runner, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil *sql.DB", ErrInvalidConfig)
	}
	cfg.ApplyDefaults()
	cfg.Migrations = normalizeMigrations(cfg.Migrations)
	dialect, err := ParseDialect(string(cfg.Dialect))
	if err != nil {
		return nil, err
	}
	cfg.Dialect = dialect
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if (dialect == DialectPostgres || dialect == DialectMySQL) && db.Stats().MaxOpenConnections == 1 {
		return nil, fmt.Errorf("%w: %s advisory lock requires at least 2 connections (lock connection + migration connection)", ErrInvalidConfig, dialect)
	}
	return &Runner{db: db, cfg: cfg, dialect: dialect}, nil
}

// normalizeMigrations 复制并补全手工构造的迁移：缺失的 checksum 按 up SQL 计算，
// NoTx 从 "-- +migrate no-transaction" 指令识别。调用方传入的切片不会被修改。
func normalizeMigrations(in []Migration) []Migration {
	out := make([]Migration, len(in))
	copy(out, in)
	for i := range out {
		if out[i].Checksum == "" {
			out[i].Checksum = checksumSQL(out[i].Up)
		}
		if !out[i].NoTx {
			out[i].NoTx = noTxPattern.MatchString(out[i].Up)
		}
	}
	return out
}

// Config 返回 Runner 使用的配置（已填充默认值）。
//
// Migrations 切片与 Runner 内部共享，调用方不得修改。
func (r *Runner) Config() Config {
	if r == nil {
		return Config{}
	}
	return r.cfg
}

// Up 依次应用所有待执行迁移；无待执行项时返回空 Result。
//
// 顺序：获取咨询锁 → 确保历史表 → 校验历史（dirty / 漂移 / 缺失）→ 校验乱序 → 逐条执行。
// 任一步失败立即停止，已成功提交的迁移保留在历史表中，可修复后重跑（幂等）。
func (r *Runner) Up(ctx context.Context) (Result, error) {
	return r.upTo(ctx, -1)
}

// UpTo 应用所有待执行迁移中版本不高于 target 的部分；target < 0 等价于 Up。
//
// 用于 expand / contract 需要外部回填的链路（例如 000006 → 回填 → 000007）：
// 先 UpTo 到 expand 版本，执行回填工具，再 Up 应用剩余收紧迁移；
// 否则一次性 Up 会在收紧迁移处因未回填行失败。
func (r *Runner) UpTo(ctx context.Context, target int64) (Result, error) {
	return r.upTo(ctx, target)
}

// upTo 是 Up / UpTo 的公共实现；target < 0 表示不限制版本。
func (r *Runner) upTo(ctx context.Context, target int64) (Result, error) {
	res := Result{Direction: DirectionUp, DryRun: r.cfg.DryRun}
	err := r.withLock(ctx, func(ctx context.Context) error {
		records, _, err := r.prepare(ctx)
		if err != nil {
			return err
		}
		pending, err := r.pending(records)
		if err != nil {
			return err
		}
		for _, m := range pending {
			// 版本上限：跳过高版本，留给外部回填完成后的下一次 Up。
			if target >= 0 && m.Version > target {
				continue
			}
			stmts, err := r.scriptStatements(m.Up)
			if err != nil {
				return fmt.Errorf("migrate: split up sql %06d_%s: %w", m.Version, m.Description, err)
			}
			if len(stmts) == 0 {
				return fmt.Errorf("%w: version %06d_%s has no executable statement", ErrInvalidSource, m.Version, m.Description)
			}
			if r.cfg.DryRun {
				step := Step{Version: m.Version, Description: m.Description, Direction: DirectionUp, Statements: len(stmts)}
				res.Steps = append(res.Steps, step)
				r.emit(Event{Kind: EventPlan, Direction: DirectionUp, Version: m.Version, Description: m.Description, Statements: len(stmts)})
				continue
			}
			step, err := r.apply(ctx, m, stmts)
			if err != nil {
				return err
			}
			res.Steps = append(res.Steps, step)
		}
		return nil
	})
	return res, err
}

// Down 回滚最近一条已应用迁移（等价于 DownSteps(ctx, 1)）。
func (r *Runner) Down(ctx context.Context) (Result, error) {
	return r.DownSteps(ctx, 1)
}

// DownSteps 回滚最近 steps 条已应用迁移；steps <= 0 返回 ErrInvalidConfig。
func (r *Runner) DownSteps(ctx context.Context, steps int) (Result, error) {
	if steps <= 0 {
		return Result{Direction: DirectionDown, DryRun: r.cfg.DryRun}, fmt.Errorf("%w: steps must be > 0", ErrInvalidConfig)
	}
	return r.down(ctx, steps, nil)
}

// DownTo 回滚到版本 version（保留 version 及更早的迁移，version=0 表示全部回滚）。
//
// version 无需已应用：只按版本号边界决定回滚范围，便于把环境对齐到指定基线。
func (r *Runner) DownTo(ctx context.Context, version int64) (Result, error) {
	if version < 0 {
		return Result{Direction: DirectionDown, DryRun: r.cfg.DryRun}, fmt.Errorf("%w: target version must be >= 0", ErrInvalidConfig)
	}
	return r.down(ctx, 0, &version)
}

func (r *Runner) down(ctx context.Context, steps int, to *int64) (Result, error) {
	res := Result{Direction: DirectionDown, DryRun: r.cfg.DryRun}
	err := r.withLock(ctx, func(ctx context.Context) error {
		records, source, err := r.prepare(ctx)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return nil
		}
		count := steps
		if to != nil {
			keep := 0
			for _, rec := range records {
				if rec.Version <= *to {
					keep++
				}
			}
			count = len(records) - keep
		}
		if count > len(records) {
			count = len(records)
		}
		for i := len(records) - 1; i >= len(records)-count; i-- {
			rec := records[i]
			m, ok := source[rec.Version]
			if !ok {
				return fmt.Errorf("%w: version %06d (%s)", ErrMissingMigration, rec.Version, rec.Description)
			}
			if strings.TrimSpace(m.Down) == "" {
				return fmt.Errorf("%w: version %06d_%s (DATA-02)", ErrMissingDown, m.Version, m.Description)
			}
			stmts, err := r.scriptStatements(m.Down)
			if err != nil {
				return fmt.Errorf("migrate: split down sql %06d_%s: %w", m.Version, m.Description, err)
			}
			if len(stmts) == 0 {
				return fmt.Errorf("%w: version %06d_%s (DATA-02)", ErrMissingDown, m.Version, m.Description)
			}
			if r.cfg.DryRun {
				res.Steps = append(res.Steps, Step{Version: m.Version, Description: m.Description, Direction: DirectionDown, Statements: len(stmts)})
				r.emit(Event{Kind: EventPlan, Direction: DirectionDown, Version: m.Version, Description: m.Description, Statements: len(stmts)})
				continue
			}
			step, err := r.revert(ctx, m, stmts)
			if err != nil {
				return err
			}
			res.Steps = append(res.Steps, step)
		}
		return nil
	})
	return res, err
}

// Status 返回当前迁移状态（只读；会幂等创建历史表）。
//
// 与 Up/Down 不同，Status 不因 dirty / 漂移 / 缺失而报错，而是通过 Status 字段报告，
// 便于运维诊断。
func (r *Runner) Status(ctx context.Context) (Status, error) {
	if err := r.ensureHistory(ctx); err != nil {
		return Status{}, err
	}
	records, err := r.loadRecords(ctx)
	if err != nil {
		return Status{}, err
	}
	source := make(map[int64]Migration, len(r.cfg.Migrations))
	for _, m := range r.cfg.Migrations {
		source[m.Version] = m
	}
	applied := make(map[int64]struct{}, len(records))
	st := Status{Applied: records}
	for _, rec := range records {
		applied[rec.Version] = struct{}{}
		if rec.Version > st.Current {
			st.Current = rec.Version
		}
		if rec.Dirty {
			st.Dirty = true
		}
		m, ok := source[rec.Version]
		if !ok {
			st.Missing = append(st.Missing, rec.Version)
			continue
		}
		if rec.Checksum != m.Checksum {
			st.Drift = append(st.Drift, rec.Version)
		}
	}
	for _, m := range r.cfg.Migrations {
		if _, ok := applied[m.Version]; !ok {
			st.Pending = append(st.Pending, m)
		}
	}
	return st, nil
}

// prepare 获取历史表状态并校验一致性（Up/Down 共用）。
func (r *Runner) prepare(ctx context.Context) ([]Record, map[int64]Migration, error) {
	if err := r.ensureHistory(ctx); err != nil {
		return nil, nil, err
	}
	records, err := r.loadRecords(ctx)
	if err != nil {
		return nil, nil, err
	}
	source := make(map[int64]Migration, len(r.cfg.Migrations))
	for _, m := range r.cfg.Migrations {
		source[m.Version] = m
	}
	if err := r.checkHistory(records, source); err != nil {
		return nil, nil, err
	}
	return records, source, nil
}

// checkHistory 拒绝执行 dirty、已应用但源文件缺失、checksum 漂移的数据库。
func (r *Runner) checkHistory(records []Record, source map[int64]Migration) error {
	var drift []string
	for _, rec := range records {
		if rec.Dirty {
			return fmt.Errorf("%w: version %06d_%s 未完成；请人工确认数据库状态后清理历史表记录", ErrDirty, rec.Version, rec.Description)
		}
		m, ok := source[rec.Version]
		if !ok {
			if r.cfg.AllowMissingApplied {
				continue
			}
			return fmt.Errorf("%w: version %06d (%s)", ErrMissingMigration, rec.Version, rec.Description)
		}
		if rec.Checksum != m.Checksum {
			drift = append(drift, fmt.Sprintf("%06d", rec.Version))
		}
	}
	if len(drift) > 0 {
		return fmt.Errorf("%w: versions %s 的 up SQL 已被修改（禁止改写已应用迁移，另加新迁移）", ErrChecksumMismatch, strings.Join(drift, ", "))
	}
	return nil
}

// pending 返回待应用迁移，并在执行前拒绝乱序补丁。
func (r *Runner) pending(records []Record) ([]Migration, error) {
	applied := make(map[int64]struct{}, len(records))
	maxApplied := int64(0)
	for _, rec := range records {
		applied[rec.Version] = struct{}{}
		if rec.Version > maxApplied {
			maxApplied = rec.Version
		}
	}
	pending := make([]Migration, 0, len(r.cfg.Migrations))
	for _, m := range r.cfg.Migrations {
		if _, ok := applied[m.Version]; ok {
			continue
		}
		if m.Version < maxApplied && !r.cfg.AllowOutOfOrder {
			return nil, fmt.Errorf("%w: version %06d_%s < applied %06d", ErrOutOfOrder, m.Version, m.Description, maxApplied)
		}
		pending = append(pending, m)
	}
	return pending, nil
}

// scriptStatements 按配置切分脚本；NoSplit 时整份脚本作为一条语句。
func (r *Runner) scriptStatements(script string) ([]string, error) {
	if r.cfg.NoSplit {
		if strings.TrimSpace(script) == "" {
			return nil, nil
		}
		return []string{script}, nil
	}
	return splitStatements(script, r.dialect == DialectMySQL)
}

// apply 执行一条 up 迁移并回调事件。
func (r *Runner) apply(ctx context.Context, m Migration, stmts []string) (Step, error) {
	started := time.Now()
	r.emit(Event{Kind: EventStart, Direction: DirectionUp, Version: m.Version, Description: m.Description, Statements: len(stmts)})
	var err error
	if m.NoTx {
		err = r.applyNoTx(ctx, m, stmts, started)
	} else {
		err = r.applyTx(ctx, m, stmts, started)
	}
	if err != nil {
		r.emit(Event{Kind: EventError, Direction: DirectionUp, Version: m.Version, Description: m.Description, Statements: len(stmts), Err: err})
		return Step{}, err
	}
	step := Step{Version: m.Version, Description: m.Description, Direction: DirectionUp, Statements: len(stmts), Duration: time.Since(started)}
	r.emit(Event{Kind: EventDone, Direction: DirectionUp, Version: m.Version, Description: m.Description, Statements: len(stmts), Duration: step.Duration})
	return step, nil
}

// applyTx 在单个事务内写入 dirty 记录、执行语句并清除 dirty。
//
// MySQL 等 DDL 隐式提交的数据库若中途失败，dirty 记录会保留，阻断后续执行。
func (r *Runner) applyTx(ctx context.Context, m Migration, stmts []string, started time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate: begin tx for %06d_%s: %w", m.Version, m.Description, err)
	}
	defer func() {
		// 忽略：Commit 成功后 Rollback 返回 ErrTxDone；失败路径由数据库回滚。
		_ = tx.Rollback()
	}()
	if _, err := tx.ExecContext(ctx, r.insertHistorySQL(),
		m.Version, m.Description, m.Checksum, time.Now().UTC(), int64(0), true); err != nil {
		return fmt.Errorf("migrate: record %06d_%s as dirty: %w", m.Version, m.Description, err)
	}
	for i, stmt := range stmts {
		if err := r.execStatement(ctx, tx, m, i, stmt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, r.finishApplySQL(), false, time.Since(started).Milliseconds(), m.Version); err != nil {
		return fmt.Errorf("%w: finalize history %06d_%s: %w", ErrStatementFailed, m.Version, m.Description, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: commit %06d_%s: %w", ErrCommitFailed, m.Version, m.Description, err)
	}
	return nil
}

// applyNoTx 用于不支持事务 DDL 的迁移（"-- +migrate no-transaction"）。
func (r *Runner) applyNoTx(ctx context.Context, m Migration, stmts []string, started time.Time) error {
	if _, err := r.db.ExecContext(ctx, r.insertHistorySQL(),
		m.Version, m.Description, m.Checksum, time.Now().UTC(), int64(0), true); err != nil {
		return fmt.Errorf("migrate: record %06d_%s as dirty: %w", m.Version, m.Description, err)
	}
	for i, stmt := range stmts {
		if err := r.execStatement(ctx, r.db, m, i, stmt); err != nil {
			return err
		}
	}
	if _, err := r.db.ExecContext(ctx, r.finishApplySQL(), false, time.Since(started).Milliseconds(), m.Version); err != nil {
		return fmt.Errorf("%w: finalize history %06d_%s: %w", ErrStatementFailed, m.Version, m.Description, err)
	}
	return nil
}

// revert 回滚一条迁移并回调事件。
func (r *Runner) revert(ctx context.Context, m Migration, stmts []string) (Step, error) {
	started := time.Now()
	r.emit(Event{Kind: EventStart, Direction: DirectionDown, Version: m.Version, Description: m.Description, Statements: len(stmts)})
	var err error
	if m.NoTx {
		err = r.revertNoTx(ctx, m, stmts)
	} else {
		err = r.revertTx(ctx, m, stmts)
	}
	if err != nil {
		r.emit(Event{Kind: EventError, Direction: DirectionDown, Version: m.Version, Description: m.Description, Statements: len(stmts), Err: err})
		return Step{}, err
	}
	step := Step{Version: m.Version, Description: m.Description, Direction: DirectionDown, Statements: len(stmts), Duration: time.Since(started)}
	r.emit(Event{Kind: EventDone, Direction: DirectionDown, Version: m.Version, Description: m.Description, Statements: len(stmts), Duration: step.Duration})
	return step, nil
}

func (r *Runner) revertTx(ctx context.Context, m Migration, stmts []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate: begin tx for %06d_%s: %w", m.Version, m.Description, err)
	}
	defer func() {
		// 忽略：Commit 成功后 Rollback 返回 ErrTxDone；失败路径由数据库回滚。
		_ = tx.Rollback()
	}()
	if err := r.markDirty(ctx, tx, m); err != nil {
		return err
	}
	for i, stmt := range stmts {
		if err := r.execStatement(ctx, tx, m, i, stmt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, r.deleteHistorySQL(), m.Version); err != nil {
		return fmt.Errorf("%w: delete history %06d_%s: %w", ErrStatementFailed, m.Version, m.Description, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: commit %06d_%s: %w", ErrCommitFailed, m.Version, m.Description, err)
	}
	return nil
}

func (r *Runner) revertNoTx(ctx context.Context, m Migration, stmts []string) error {
	if err := r.markDirty(ctx, r.db, m); err != nil {
		return err
	}
	for i, stmt := range stmts {
		if err := r.execStatement(ctx, r.db, m, i, stmt); err != nil {
			return err
		}
	}
	if _, err := r.db.ExecContext(ctx, r.deleteHistorySQL(), m.Version); err != nil {
		return fmt.Errorf("%w: delete history %06d_%s: %w", ErrStatementFailed, m.Version, m.Description, err)
	}
	return nil
}

// markDirty 在回滚前标记 dirty，便于非事务数据库失败后诊断。
func (r *Runner) markDirty(ctx context.Context, ex execer, m Migration) error {
	result, err := ex.ExecContext(ctx, r.markDirtySQL(), true, m.Version)
	if err != nil {
		return fmt.Errorf("migrate: mark %06d_%s dirty: %w", m.Version, m.Description, err)
	}
	affected, err := result.RowsAffected()
	if err == nil && affected == 0 {
		return fmt.Errorf("%w: version %06d_%s", ErrNotApplied, m.Version, m.Description)
	}
	return nil
}

// execStatement 执行单条语句并附加 StatementTimeout。
func (r *Runner) execStatement(ctx context.Context, ex execer, m Migration, index int, stmt string) error {
	execCtx := ctx
	if r.cfg.StatementTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, r.cfg.StatementTimeout)
		defer cancel()
	}
	if _, err := ex.ExecContext(execCtx, stmt); err != nil {
		return fmt.Errorf("%w: %06d_%s statement %d: %w", ErrStatementFailed, m.Version, m.Description, index+1, err)
	}
	return nil
}

// lockReleaseTimeout 限制释放咨询锁的等待时间，避免数据库异常时阻塞进程退出。
const lockReleaseTimeout = 5 * time.Second

// withLock 在咨询锁保护下执行 fn；dry-run 不加锁也不执行迁移 SQL。
func (r *Runner) withLock(ctx context.Context, fn func(context.Context) error) error {
	if r.cfg.DryRun {
		return fn(ctx)
	}
	release, err := r.acquireLock(ctx)
	if err != nil {
		return err
	}
	defer func() {
		// 忽略：释放失败时连接关闭会终止会话，数据库自动释放会话级锁。
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockReleaseTimeout)
		defer cancel()
		_ = release(releaseCtx)
	}()
	return fn(ctx)
}

func (r *Runner) emit(e Event) {
	if r.cfg.Observer != nil {
		r.cfg.Observer(e)
	}
}
