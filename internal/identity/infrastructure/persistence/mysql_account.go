package persistence

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// accountSelectColumns 是账号表的显式列清单（禁止 SELECT *，DATA-04）。
const accountSelectColumns = `id, channel, subject, subject_index, subject_ciphertext, display_name,
	status, token_version, version, merchant_id, password_updated_at, locked_until,
	must_change_password, created_at, updated_at, deleted_at`

// accountListCountCap 是账号列表总数统计的扫描上限，避免前置通配符导致全表 COUNT。
const accountListCountCap = 10000

// AccountRepositoryOption 配置 SQL 账号仓储的可选能力。
type AccountRepositoryOption func(*SQLAccountRepository)

// WithSubjectProtector 注入 PII 主体保护器，启用 S1/S2 双写双读（IDN-GAP-P1-06）。
//
// 未注入时保持纯明文路径，行为与改造前一致；注入后写路径同时写 index/ciphertext，
// 读路径优先按 index 查找、未命中回退明文。
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
// S2 双读：配置 PII 保护器时先按 subject_index 查找；未命中（存量未回填）回退明文查询。
func (r *SQLAccountRepository) FindBySubject(ctx context.Context, channel domain.Channel, merchantID, subject string) (*domain.Account, error) {
	if r.protector != nil {
		index := r.protector.Index(channel, merchantID, subject)
		if index != "" {
			row := r.exec(ctx).QueryRowContext(ctx,
				`SELECT `+accountSelectColumns+` FROM identity_account
				 WHERE channel = ? AND merchant_id = ? AND subject_index = ?`,
				string(channel), merchantID, index)
			account, err := scanAccount(row)
			if err == nil {
				if decodeErr := r.decodeSubject(account); decodeErr != nil {
					return nil, decodeErr
				}
				return account, nil
			}
			if !errors.Is(err, apperr.ErrNotFound) && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			// 未命中：可能是存量行尚未回填 index，回退明文路径（双读）。
		}
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+accountSelectColumns+` FROM identity_account
		 WHERE channel = ? AND merchant_id = ? AND subject = ?`,
		string(channel), merchantID, domain.NormalizeSubject(subject))
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
// S1 双写：配置保护器时同时写入 subject_index 与 subject_ciphertext，明文列继续保留。
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
		(id, channel, subject, subject_index, subject_ciphertext, display_name, status,
		 token_version, version, merchant_id, password_updated_at, locked_until,
		 must_change_password, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		accountID, string(account.Channel), account.Subject, nullString(index), nullBytes(ciphertext),
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
		args = append(args, "%"+escapeLikePattern(keyword)+"%")
	}
	where := strings.Join(conditions, " AND ")

	// 关键词为前置通配符，无法命中索引；总数统计限制扫描上限，避免单次请求拖垮 DB。
	// 返回的 total 在超过上限时为上限值，前端应展示“约 N 条”。
	var total int
	countQuery := `SELECT COUNT(*) FROM (SELECT 1 FROM identity_account WHERE ` + where +
		` LIMIT ?) AS capped`
	if err := r.exec(ctx).QueryRowContext(ctx, countQuery,
		append(args, accountListCountCap)...).Scan(&total); err != nil {
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

// RebindSubject 更新登录主体；channel / merchant_id 仍不可变。
//
// S1 双写：同时更新 subject_index 与 subject_ciphertext；未配置保护器时保持原列不变。
func (r *SQLAccountRepository) RebindSubject(ctx context.Context, account *domain.Account, expectedVersion int64) error {
	if account == nil {
		return apperr.BadRequest("account is required")
	}
	accountID, err := parseID(account.ID)
	if err != nil {
		return err
	}
	if r.protector == nil {
		result, execErr := r.exec(ctx).ExecContext(ctx, `UPDATE identity_account
			SET subject = ?, version = ?, updated_at = ?
			WHERE id = ? AND version = ?`,
			account.Subject, account.Version, account.UpdatedAt.UTC(), accountID, expectedVersion)
		if execErr != nil {
			return conflictOnDuplicate(execErr, "account subject already exists in this channel")
		}
		return r.checkUpdateResult(ctx, result, accountID)
	}
	index, ciphertext, err := r.protectSubject(account)
	if err != nil {
		return err
	}
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_account
		SET subject = ?, subject_index = ?, subject_ciphertext = ?, version = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		account.Subject, nullString(index), nullBytes(ciphertext),
		account.Version, account.UpdatedAt.UTC(), accountID, expectedVersion)
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

// protectSubject 计算主体索引与密文；未配置保护器时返回空值。
func (r *SQLAccountRepository) protectSubject(account *domain.Account) (string, []byte, error) {
	if r.protector == nil {
		return "", nil, nil
	}
	index := r.protector.Index(account.Channel, account.MerchantID, account.Subject)
	ciphertext, err := r.protector.Encrypt(account.ID, account.Subject)
	if err != nil {
		return "", nil, err
	}
	return index, ciphertext, nil
}

// decodeSubject 用密文回填明文主体；密文为空（未回填）时保留数据库明文。
func (r *SQLAccountRepository) decodeSubject(account *domain.Account) error {
	if r.protector == nil || len(account.SubjectCiphertext) == 0 {
		return nil
	}
	plaintext, err := r.protector.Decrypt(account.ID, account.SubjectCiphertext)
	if err != nil {
		return err
	}
	account.Subject = plaintext
	return nil
}

// BackfillSubjectProtection 为存量账号回填 subject_index 与 subject_ciphertext（S3）。
//
// 按主键顺序批量扫描缺失保护列的行，逐行计算后更新；可重复执行（幂等），
// 中断后再次运行会从剩余缺失行继续。返回本次实际更新的行数。
func (r *SQLAccountRepository) BackfillSubjectProtection(ctx context.Context, limit int) (int, error) {
	if r.protector == nil {
		return 0, apperr.BadRequest("subject protector is not configured")
	}
	if r.sql == nil {
		return 0, apperr.Unavailable("identity account repository is not configured")
	}
	if limit <= 0 {
		limit = 500
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT id, channel, merchant_id, subject
		FROM identity_account
		WHERE subject_index IS NULL OR subject_ciphertext IS NULL
		ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

	type pending struct {
		id         int64
		channel    domain.Channel
		merchantID string
		subject    string
	}
	items := make([]pending, 0, limit)
	for rows.Next() {
		var item pending
		if scanErr := rows.Scan(&item.id, &item.channel, &item.merchantID, &item.subject); scanErr != nil {
			return 0, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	updated := 0
	for _, item := range items {
		accountID := strconv.FormatInt(item.id, 10)
		index := r.protector.Index(item.channel, item.merchantID, item.subject)
		ciphertext, encErr := r.protector.Encrypt(accountID, item.subject)
		if encErr != nil {
			return updated, encErr
		}
		if _, execErr := r.sql.ExecContext(ctx, `UPDATE identity_account
			SET subject_index = ?, subject_ciphertext = ?
			WHERE id = ? AND (subject_index IS NULL OR subject_ciphertext IS NULL)`,
			nullString(index), nullBytes(ciphertext), item.id); execErr != nil {
			return updated, execErr
		}
		updated++
	}
	return updated, nil
}

// scanAccount 扫描一行账号；可空列允许 NULL。
//
// subject_index / subject_ciphertext 在 S1/S2 阶段允许 NULL（存量未回填），
// 必须用可空类型扫描，否则 NULL 会让整个账号读取失败。
func scanAccount(row scanRow) (*domain.Account, error) {
	var (
		account           domain.Account
		subjectIndex      sql.NullString
		passwordUpdatedAt sql.NullTime
		lockedUntil       sql.NullTime
		deletedAt         sql.NullTime
	)
	err := row.Scan(
		&account.ID, &account.Channel, &account.Subject, &subjectIndex,
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
