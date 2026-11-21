package persistence

import (
	"context"
	"database/sql"
	"errors"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
)

// InstallRepository 是 domain.InstallRepository 的 MySQL 实现。
type InstallRepository struct {
	db *sql.DB
}

var _ domain.InstallRepository = (*InstallRepository)(nil)

// NewInstallRepository 构造安装态仓储。
func NewInstallRepository(sqlDB *sql.DB) (*InstallRepository, error) {
	if sqlDB == nil {
		return nil, errors.New("configcenter install repository requires a database handle")
	}
	return &InstallRepository{db: sqlDB}, nil
}

const installColumns = "env, status, owner, lease_until, version, last_error, updated_at, completed_at"

// Get 返回安装态。
func (r *InstallRepository) Get(ctx context.Context, env string) (domain.InstallState, error) {
	row := queryFrom(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+installColumns+` FROM config_install_state WHERE env = ?`, env)
	return scanInstall(row)
}

// Ensure 幂等插入初始行。
func (r *InstallRepository) Ensure(ctx context.Context, state domain.InstallState) (domain.InstallState, error) {
	if err := state.Validate(); err != nil {
		return domain.InstallState{}, err
	}
	_, err := execFrom(ctx, r.db).ExecContext(ctx, `
INSERT INTO config_install_state
  (env, status, owner, lease_until, version, last_error, updated_at, completed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE env = env`,
		state.Env, string(state.Status), state.Owner, nullTimePtr(state.LeaseUntil),
		state.Version, state.LastError, state.UpdatedAt.UTC(), nullTimePtr(state.CompletedAt))
	if err != nil {
		return domain.InstallState{}, err
	}
	return r.Get(ctx, state.Env)
}

// CompareAndSwap 以 expectedVersion 做 CAS。
func (r *InstallRepository) CompareAndSwap(ctx context.Context, next domain.InstallState, expectedVersion int64) (domain.InstallState, error) {
	if err := next.Validate(); err != nil {
		return domain.InstallState{}, err
	}
	res, err := execFrom(ctx, r.db).ExecContext(ctx, `
UPDATE config_install_state
SET status = ?, owner = ?, lease_until = ?, version = ?, last_error = ?,
    updated_at = ?, completed_at = ?
WHERE env = ? AND version = ?`,
		string(next.Status), next.Owner, nullTimePtr(next.LeaseUntil),
		expectedVersion+1, next.LastError, next.UpdatedAt.UTC(), nullTimePtr(next.CompletedAt),
		next.Env, expectedVersion)
	if err != nil {
		return domain.InstallState{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return domain.InstallState{}, err
	}
	if affected == 0 {
		return domain.InstallState{}, domain.ErrVersionConflict
	}
	return r.Get(ctx, next.Env)
}

type scannable interface {
	Scan(dest ...any) error
}

func scanInstall(row scannable) (domain.InstallState, error) {
	var (
		state       domain.InstallState
		status      string
		leaseUntil  sql.NullTime
		completedAt sql.NullTime
	)
	err := row.Scan(&state.Env, &status, &state.Owner, &leaseUntil, &state.Version,
		&state.LastError, &state.UpdatedAt, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.InstallState{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.InstallState{}, err
	}
	state.Status = domain.InstallStatus(status)
	if leaseUntil.Valid {
		t := leaseUntil.Time.UTC()
		state.LeaseUntil = &t
	}
	if completedAt.Valid {
		t := completedAt.Time.UTC()
		state.CompletedAt = &t
	}
	state.UpdatedAt = state.UpdatedAt.UTC()
	return state, nil
}
