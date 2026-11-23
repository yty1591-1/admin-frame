package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
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

const installColumns = "env, status, mode, owner, repair_keys, lease_until, version, last_error, updated_at, completed_at"

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
  (env, status, mode, owner, repair_keys, lease_until, version, last_error, updated_at, completed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE env = env`,
		state.Env, string(state.Status), string(state.Mode), state.Owner,
		marshalRepairKeys(state.RepairKeys), nullTimePtr(state.LeaseUntil),
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
SET status = ?, mode = ?, owner = ?, repair_keys = ?, lease_until = ?, version = ?, last_error = ?,
    updated_at = ?, completed_at = ?
WHERE env = ? AND version = ?`,
		string(next.Status), string(next.Mode), next.Owner, marshalRepairKeys(next.RepairKeys),
		nullTimePtr(next.LeaseUntil), expectedVersion+1, next.LastError, next.UpdatedAt.UTC(),
		nullTimePtr(next.CompletedAt), next.Env, expectedVersion)
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
		mode        string
		repairKeys  sql.NullString
		leaseUntil  sql.NullTime
		completedAt sql.NullTime
	)
	err := row.Scan(&state.Env, &status, &mode, &state.Owner, &repairKeys, &leaseUntil,
		&state.Version, &state.LastError, &state.UpdatedAt, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.InstallState{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.InstallState{}, err
	}
	state.Status = domain.InstallStatus(status)
	state.Mode = domain.InstallMode(mode)
	if state.Mode == "" {
		// 000038 之前写入的行没有 mode 列值；按首次安装解释。
		state.Mode = domain.InstallModeFirstInstall
	}
	if repairKeys.Valid && repairKeys.String != "" {
		if err := json.Unmarshal([]byte(repairKeys.String), &state.RepairKeys); err != nil {
			return domain.InstallState{}, err
		}
	}
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

// marshalRepairKeys 把白名单编码成 JSON 列值；空列表写 NULL。
func marshalRepairKeys(keys []string) any {
	if len(keys) == 0 {
		return nil
	}
	encoded, err := json.Marshal(keys)
	if err != nil {
		// []string 编码不会失败；保底返回 NULL 交由上层校验暴露。
		return nil
	}
	return string(encoded)
}
