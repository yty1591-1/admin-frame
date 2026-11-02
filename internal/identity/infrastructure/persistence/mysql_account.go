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
//
// S5 起不再存在明文 subject 列；主体由 subject_ciphertext 解密回填。
const accountSelectColumns = `id, channel, subject_index, subject_ciphertext, display_name,
	status, token_version, version, merchant_id, password_updated_at, locked_until,
	must_change_password, created_at, updated_at, deleted_at`

// accountListCountCap 是账号列表总数统计的扫描上限，避免前置通配符导致全表 COUNT。
const accountListCountCap = 10000

// AccountRepositoryOption 配置 SQL 账号仓储的可选能力。
type AccountRepositoryOption func(*SQLAccountRepository)

// WithSubjectProtector 注入 PII 主体保护器（S5：明文列已删除，保护器为必需能力）。
//
// 未注入时账号写路径与主体查找失败关闭，避免退回明文存储。
func WithSubjectProtector(protector SubjectProtector) AccountRepositoryOption {
	return func(r *SQLAccountRepository) {
		if protector != nil {
			r.protector = protector
		}
	}
}

// SQLAccountRepository 是 MySQL 8.0 的账号仓储实现。
//
// 依赖 DSN 设置 `parseTime=true&loc=UTC`，否则 DATETIME(3) 无法扫描为 time.Time。
type SQLAccountRepository struct {
	sql       *sql.DB
	protector SubjectProtector
}

// 编译期断言：实现必须满足领域端口。
var _ domain.AccountRepository = (*SQLAccountRepository)(nil)

// NewSQLAccountRepository 基于已打开的 *sql.DB 创建账号仓储。
func NewSQLAccountRepository(sqlDB *sql.DB, opts ...AccountRepositoryOption) *SQLAccountRepository {
	repository := &SQLAccountRepository{sql: sqlDB}
	for _, opt := range opts {
		if opt != nil {
			opt(repository)
		}
	}
	return repository
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
	if err := r.decodeSubject(account); err != nil {
		return nil, err
	}
	return account, nil
}

// FindBySubject 按端别 + 规范化主体加载账号；软删账号仍返回，用于阻止 subject 复用。
//
// S5：只按 subject_index 查找，不再回退明文；未配置保护器时失败关闭。
func (r *SQLAccountRepository) FindBySubject(ctx context.Context, channel domain.Channel, merchantID, subject string) (*domain.Account, error) {
	if r.protector == nil {
		return nil, apperr.Unavailable("subject protector is not configured")
	}
	index := r.protector.Index(channel, merchantID, subject)
	if index == "" {
		return nil, apperr.Internal("subject index is empty", nil)
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+accountSelectColumns+` FROM identity_account
		 WHERE channel = ? AND merchant_id = ? AND subject_index = ?`,
		string(channel), merchantID, index)
	account, err := scanAccount(row)
	if err != nil {
		return nil, resolveNotFound(err, "account not found")
	}
	if err := r.decodeSubject(account); err != nil {
		return nil, err
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
//
// S5：只写 subject_index 与 subject_ciphertext，不再写明文列。
func (r *SQLAccountRepository) insert(ctx context.Context, account *domain.Account) error {
	accountID, err := parseID(account.ID)
	if err != nil {
		return err
	}
	index, ciphertext, err := r.protectSubject(account)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx).ExecContext(ctx, `INSERT INTO identity_account
		(id, channel, subject_index, subject_ciphertext, display_name, status,
		 token_version, version, merchant_id, password_updated_at, locked_until,
		 must_change_password, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		accountID, string(account.Channel), index, ciphertext,
		account.DisplayName, string(account.Status),
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
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
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
	if filter.MerchantID != "" {
		conditions = append(conditions, "merchant_id = ?")
		args = append(args, filter.MerchantID)
	}

	// PERF-07：精确主体走 subject_index 索引，避免关键词路径的全量扫描。
	if exact := domain.NormalizeSubject(filter.SubjectExact); exact != "" {
		if r.protector == nil {
			return nil, 0, apperr.Unavailable("subject protector is not configured")
		}
		index := r.protector.Index(filter.Channel, filter.MerchantID, exact)
		if index == "" {
			return nil, 0, apperr.Internal("subject index is empty", nil)
		}
		conditions = append(conditions, "subject_index = ?")
		args = append(args, index)
	}

	where := strings.Join(conditions, " AND ")

	// S5 后不再有明文主体列，关键词子串匹配无法下推到 SQL；
	// 仅在该条件存在时按上限扫描并在内存解密过滤（管理端列表量级可控）。
	if exact := domain.NormalizeSubject(filter.SubjectExact); exact == "" {
		if keyword := domain.NormalizeSubject(filter.Keyword); keyword != "" {
			return r.listByKeyword(ctx, where, args, keyword, limit, offset)
		}
	}

	// 总数统计限制扫描上限，避免无界 COUNT。
	var total int
	countQuery := `SELECT COUNT(*) FROM (SELECT 1 FROM identity_account WHERE ` + where +
		` LIMIT ?) AS capped`
	if err := r.exec(ctx).QueryRowContext(ctx, countQuery,
		append(args, accountListCountCap)...).Scan(&total); err != nil {
		return nil, 0, err
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
		if decodeErr := r.decodeSubject(account); decodeErr != nil {
			return nil, 0, decodeErr
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return accounts, total, nil
}

// listByKeyword 在解密后按主体子串过滤，再在内存中分页。
//
// 扫描上限 accountListCountCap；超过上限时 total 为上限值，前端应展示“约 N 条”。
func (r *SQLAccountRepository) listByKeyword(
	ctx context.Context,
	where string,
	args []any,
	keyword string,
	limit, offset int,
) ([]*domain.Account, int, error) {
	query := `SELECT ` + accountSelectColumns + ` FROM identity_account WHERE ` + where +
		` ORDER BY created_at DESC, id DESC LIMIT ?`
	rows, err := r.exec(ctx).QueryContext(ctx, query, append(args, accountListCountCap)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	matched := make([]*domain.Account, 0, limit)
	for rows.Next() {
		account, scanErr := scanAccount(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		if decodeErr := r.decodeSubject(account); decodeErr != nil {
			return nil, 0, decodeErr
		}
		if strings.Contains(domain.NormalizeSubject(account.Subject), keyword) {
			matched = append(matched, account)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	total := len(matched)
	if offset >= total {
		return []*domain.Account{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return matched[offset:end], total, nil
}

// RebindSubject 更新登录主体；channel / merchant_id 仍不可变。
//
// S5：只更新 subject_index 与 subject_ciphertext，不存在明文列可写。
func (r *SQLAccountRepository) RebindSubject(ctx context.Context, account *domain.Account, expectedVersion int64) error {
	if account == nil {
		return apperr.BadRequest("account is required")
	}
	accountID, err := parseID(account.ID)
	if err != nil {
		return err
	}
	index, ciphertext, err := r.protectSubject(account)
	if err != nil {
		return err
	}
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_account
		SET subject_index = ?, subject_ciphertext = ?, version = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		index, ciphertext, account.Version, account.UpdatedAt.UTC(), accountID, expectedVersion)
	if err != nil {
		return conflictOnDuplicate(err, "account subject already exists in this channel")
	}
	return r.checkUpdateResult(ctx, result, accountID)
}

// checkUpdateResult 把受影响行数为 0 的更新区分「不存在」与「版本冲突」。
func (r *SQLAccountRepository) checkUpdateResult(ctx context.Context, result sql.Result, accountID int64) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	return r.versionFailure(ctx, accountID, "account version conflict", "account not found")
}

// protectSubject 计算主体索引与密文；未配置保护器时失败关闭。
func (r *SQLAccountRepository) protectSubject(account *domain.Account) (string, []byte, error) {
	if r.protector == nil {
		return "", nil, apperr.Unavailable("subject protector is not configured")
	}
	index := r.protector.Index(account.Channel, account.MerchantID, account.Subject)
	ciphertext, err := r.protector.Encrypt(account.ID, account.Subject)
	if err != nil {
		return "", nil, err
	}
	return index, ciphertext, nil
}

// decodeSubject 用密文回填明文主体；S5 后密文必须存在，缺失即失败关闭。
func (r *SQLAccountRepository) decodeSubject(account *domain.Account) error {
	if r.protector == nil {
		return apperr.Unavailable("subject protector is not configured")
	}
	if len(account.SubjectCiphertext) == 0 {
		return apperr.Internal("account subject ciphertext is missing", nil)
	}
	plaintext, err := r.protector.Decrypt(account.ID, account.SubjectCiphertext)
	if err != nil {
		return err
	}
	account.Subject = plaintext
	return nil
}

// scanAccount 扫描一行账号；可空列允许 NULL。
//
// S5 后 subject_index / subject_ciphertext 为 NOT NULL，但仍用可空类型扫描，
// 便于在升级窗口容忍旧行；主体明文统一由 decodeSubject 解密回填。
func scanAccount(row scanRow) (*domain.Account, error) {
	var (
		account           domain.Account
		subjectIndex      sql.NullString
		passwordUpdatedAt sql.NullTime
		lockedUntil       sql.NullTime
		deletedAt         sql.NullTime
	)
	err := row.Scan(
		&account.ID, &account.Channel, &subjectIndex,
		&account.SubjectCiphertext, &account.DisplayName, &account.Status,
		&account.TokenVersion, &account.Version, &account.MerchantID,
		&passwordUpdatedAt, &lockedUntil, &account.MustChangePassword,
		&account.CreatedAt, &account.UpdatedAt, &deletedAt,
	)
	if err != nil {
		return nil, err
	}
	account.SubjectIndex = subjectIndex.String
	account.CreatedAt = account.CreatedAt.UTC()
	account.UpdatedAt = account.UpdatedAt.UTC()
	account.PasswordUpdatedAt = nullableTime(passwordUpdatedAt)
	account.LockedUntil = nullableTime(lockedUntil)
	account.DeletedAt = nullableTime(deletedAt)
	return &account, nil
}
