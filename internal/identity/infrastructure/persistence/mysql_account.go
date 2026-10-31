package persistence

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// accountSelectColumns 是账号表的显式列清单（禁止 SELECT *，DATA-04）。
const accountSelectColumns = `id, channel, subject, display_name, status, token_version, version, merchant_id,
	password_updated_at, locked_until, must_change_password, created_at, updated_at, deleted_at`

// SQLAccountRepository 是 MySQL 8.0 的账号仓储实现。
//
// 依赖 DSN 设置 `parseTime=true&loc=UTC`，否则 DATETIME(3) 无法扫描为 time.Time。
type SQLAccountRepository struct {
	sql *sql.DB
}

// 编译期断言：实现必须满足领域端口。
var _ domain.AccountRepository = (*SQLAccountRepository)(nil)

// NewSQLAccountRepository 基于已打开的 *sql.DB 创建账号仓储。
func NewSQLAccountRepository(sqlDB *sql.DB) *SQLAccountRepository {
	return &SQLAccountRepository{sql: sqlDB}
}

// exec 返回当前请求应使用的执行器：事务上下文优先。
func (r *SQLAccountRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// FindByID 按主键加载账号。
func (r *SQLAccountRepository) FindByID(ctx context.Context, id string) (*domain.Account, error) {
	accountID, err := parseID(id)
	if err != nil {
		return nil, err
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+accountSelectColumns+` FROM identity_account WHERE id = ?`, accountID)
	account, err := scanAccount(row)
	if err != nil {
		return nil, resolveNotFound(err, "account not found")
	}
	return account, nil
}

// FindBySubject 按端别 + 规范化主体加载账号；软删账号仍返回，用于阻止 subject 复用。
func (r *SQLAccountRepository) FindBySubject(ctx context.Context, channel domain.Channel, subject string) (*domain.Account, error) {
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+accountSelectColumns+` FROM identity_account WHERE channel = ? AND subject = ?`,
		string(channel), domain.NormalizeSubject(subject))
	account, err := scanAccount(row)
	if err != nil {
		return nil, resolveNotFound(err, "account not found")
	}
	return account, nil
}

// Save 创建（expectedVersion=0）或更新账号。
func (r *SQLAccountRepository) Save(ctx context.Context, account *domain.Account, expectedVersion int64) error {
	if account == nil {
		return apperr.BadRequest("account is required")
	}
	if r.sql == nil {
		return apperr.Unavailable("identity account repository is not configured")
	}
	if expectedVersion == 0 {
		return r.insert(ctx, account)
	}
	return r.update(ctx, account, expectedVersion)
}

// insert 写入新账号；唯一键冲突映射为 40900。
func (r *SQLAccountRepository) insert(ctx context.Context, account *domain.Account) error {
	accountID, err := parseID(account.ID)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx).ExecContext(ctx, `INSERT INTO identity_account
		(id, channel, subject, display_name, status, token_version, version, merchant_id,
		 password_updated_at, locked_until, must_change_password, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		accountID, string(account.Channel), account.Subject, account.DisplayName, string(account.Status),
		account.TokenVersion, account.Version, account.MerchantID,
		nullTime(account.PasswordUpdatedAt), nullTime(account.LockedUntil), account.MustChangePassword,
		account.CreatedAt.UTC(), account.UpdatedAt.UTC(), nullTime(account.DeletedAt))
	return conflictOnDuplicate(err, "account subject already exists in this channel")
}

// update 以乐观锁更新可变态字段；channel / subject / merchant_id 不参与 SET。
func (r *SQLAccountRepository) update(ctx context.Context, account *domain.Account, expectedVersion int64) error {
	accountID, err := parseID(account.ID)
	if err != nil {
		return err
	}
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_account
		SET display_name = ?, status = ?, token_version = ?, version = ?, password_updated_at = ?,
		    locked_until = ?, must_change_password = ?, updated_at = ?, deleted_at = ?
		WHERE id = ? AND version = ?`,
		account.DisplayName,
		string(account.Status), account.TokenVersion, account.Version,
		nullTime(account.PasswordUpdatedAt), nullTime(account.LockedUntil), account.MustChangePassword,
		account.UpdatedAt.UTC(), nullTime(account.DeletedAt), accountID, expectedVersion)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	return r.versionFailure(ctx, accountID, "account version conflict", "account not found")
}

// versionFailure 区分「不存在」与「版本冲突」，避免把并发失败误报为 404。
func (r *SQLAccountRepository) versionFailure(ctx context.Context, accountID int64, conflictMessage, notFoundMessage string) error {
	var current int64
	err := r.exec(ctx).QueryRowContext(ctx, `SELECT version FROM identity_account WHERE id = ?`, accountID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.NotFound(notFoundMessage)
	}
	if err != nil {
		return err
	}
	return apperr.Conflict(conflictMessage)
}

// List 按端别 / 状态 / 关键词分页列出账号（含软删），按创建时间倒序。
func (r *SQLAccountRepository) List(ctx context.Context, filter domain.AccountListFilter) ([]*domain.Account, int, error) {
	if filter.Channel == "" {
		return nil, 0, apperr.BadRequest("channel is required")
	}
	if err := filter.Channel.Validate(); err != nil {
		return nil, 0, err
	}
	conditions := []string{"channel = ?"}
	args := []any{string(filter.Channel)}
	if filter.Status != "" {
		if err := filter.Status.Validate(); err != nil {
			return nil, 0, err
		}
		conditions = append(conditions, "status = ?")
		args = append(args, string(filter.Status))
	}
	if keyword := domain.NormalizeSubject(filter.Keyword); keyword != "" {
		conditions = append(conditions, "subject LIKE ?")
		args = append(args, "%"+keyword+"%")
	}
	where := strings.Join(conditions, " AND ")

	var total int
	if err := r.exec(ctx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM identity_account WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	query := `SELECT ` + accountSelectColumns + ` FROM identity_account WHERE ` + where +
		` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	rows, err := r.exec(ctx).QueryContext(ctx, query, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	accounts := make([]*domain.Account, 0, limit)
	for rows.Next() {
		account, scanErr := scanAccount(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return accounts, total, nil
}

// RebindSubject 更新登录主体；channel / merchant_id 仍不可变。
func (r *SQLAccountRepository) RebindSubject(ctx context.Context, account *domain.Account, expectedVersion int64) error {
	if account == nil {
		return apperr.BadRequest("account is required")
	}
	accountID, err := parseID(account.ID)
	if err != nil {
		return err
	}
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_account
		SET subject = ?, version = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		account.Subject, account.Version, account.UpdatedAt.UTC(), accountID, expectedVersion)
	if err != nil {
		return conflictOnDuplicate(err, "account subject already exists in this channel")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	return r.versionFailure(ctx, accountID, "account version conflict", "account not found")
}

// scanAccount 扫描一行账号；可空列允许 NULL。
func scanAccount(row scanRow) (*domain.Account, error) {
	var (
		account           domain.Account
		passwordUpdatedAt sql.NullTime
		lockedUntil       sql.NullTime
		deletedAt         sql.NullTime
	)
	err := row.Scan(
		&account.ID, &account.Channel, &account.Subject, &account.DisplayName, &account.Status,
		&account.TokenVersion, &account.Version, &account.MerchantID,
		&passwordUpdatedAt, &lockedUntil, &account.MustChangePassword,
		&account.CreatedAt, &account.UpdatedAt, &deletedAt,
	)
	if err != nil {
		return nil, err
	}
	account.CreatedAt = account.CreatedAt.UTC()
	account.UpdatedAt = account.UpdatedAt.UTC()
	account.PasswordUpdatedAt = nullableTime(passwordUpdatedAt)
	account.LockedUntil = nullableTime(lockedUntil)
	account.DeletedAt = nullableTime(deletedAt)
	return &account, nil
}
