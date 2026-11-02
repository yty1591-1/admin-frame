// Package bootstrap 在 A 端身份模块启动时幂等创建系统默认平台管理员。
//
// 迁移脚本无法用应用密钥加密登录主体，因此账号、密码凭据、角色绑定与审计事件
// 统一由 identityruntime 在装配期（数据库迁移完成后）于同一事务内写入。
// 重复启动不会覆盖已有账号、改密或重复写审计。
package bootstrap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
)

const (
	// DefaultSubject 是系统默认的平台管理员登录名。
	DefaultSubject = "admin@example.com"

	// 固定主键沿用原 000005 迁移的保留段，避免与运行时雪花 ID 冲突。
	bootstrapAccountID    int64 = 1900000000000000002
	bootstrapCredentialID int64 = 1900000000000000003
	bootstrapBindingID    int64 = 1900000000000000200
	bootstrapEventID            = "1900000000000000300"

	// bootstrapChannel / bootstrapMerchantID 固定为 A 端平台账号。
	bootstrapChannel    = "admin"
	bootstrapMerchantID = ""

	// bootstrapPasswordHash 是默认演练口令 Admin#Bootstrap2026 的 Argon2id 编码值。
	// 公开演练口令必须配合 must_change_password=1，首次登录先改密。
	bootstrapPasswordHash = "$argon2id$v=19$m=19456,t=2,p=1$bGxqpo2HjthAlN+YLfpSkA$4FQGp1ifQnkGYCoMDYwN/pmejWVtI5K3Rh/8/uip//I"

	auditTopic       = "identity.audit"
	auditEventType   = "audit.event"
	auditTraceID     = "bootstrap-admin"
	bootstrapActorID = "system:bootstrap"
)

// Options 是管理员引导的可调参数。
type Options struct {
	// Subject 登录主体；空值使用 DefaultSubject，写入前统一 trim + 小写。
	Subject string
	// Service 写入审计事件的服务名，通常取 cfg.Service。
	Service string
	// Now 注入时钟，零值取当前 UTC 时间。
	Now time.Time
}

// Result 描述本次引导结果。
type Result struct {
	// AccountID 管理员账号主键。
	AccountID int64
	// Created 为 true 表示本次新建了账号；false 表示复用已有账号。
	Created bool
}

// EnsureAdmin 在单事务内幂等创建系统默认平台管理员。
//
// protector 必填：缺少保护器时无法计算 subject_index 与密文，调用方应显式失败。
// 前置：migrations 已全部执行（尤其 000002 / 000003 已创建 iam_role 与 system_admin）。
func EnsureAdmin(ctx context.Context, sqlDB *sql.DB, protector persistence.SubjectProtector, opts Options) (Result, error) {
	if sqlDB == nil {
		return Result{}, errors.New("bootstrap: nil *sql.DB")
	}
	if protector == nil {
		return Result{}, errors.New("bootstrap: subject protector is required")
	}

	subject := opts.Subject
	if subject == "" {
		subject = DefaultSubject
	}
	service := opts.Service
	if service == "" {
		service = "identity"
	}
	subject = domain.NormalizeSubject(subject)
	if subject == "" {
		return Result{}, errors.New("bootstrap: subject is required")
	}
	if len(subject) > domain.MaxSubjectLen {
		return Result{}, fmt.Errorf("bootstrap: subject exceeds %d bytes", domain.MaxSubjectLen)
	}
	index := protector.Index(domain.ChannelAdmin, bootstrapMerchantID, subject)
	if index == "" {
		return Result{}, errors.New("bootstrap: subject index is empty")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	accountID := bootstrapAccountID

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("bootstrap: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	created, err := ensureAccount(ctx, tx, protector, accountID, index, subject, now)
	if err != nil {
		return Result{}, err
	}
	if !created {
		existingID, existingErr := findAccountID(ctx, tx, index)
		if existingErr != nil {
			return Result{}, existingErr
		}
		accountID = existingID
	}

	if err := ensureCredential(ctx, tx, accountID, now); err != nil {
		return Result{}, err
	}

	roleID, err := findSystemAdminRole(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	if err := ensureBinding(ctx, tx, accountID, roleID, now); err != nil {
		return Result{}, err
	}
	if created {
		if err := writeAudit(ctx, tx, accountID, index, service, now); err != nil {
			return Result{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("bootstrap: commit: %w", err)
	}
	return Result{AccountID: accountID, Created: created}, nil
}

// ensureAccount 在账号不存在时创建；存在时返回 false。软删账号视为已占用唯一键，直接报错。
func ensureAccount(ctx context.Context, tx *sql.Tx, protector persistence.SubjectProtector, accountID int64, index, subject string, now time.Time) (bool, error) {
	var (
		existingID int64
		deletedAt  sql.NullTime
	)
	err := tx.QueryRowContext(ctx, `SELECT id, deleted_at FROM identity_account
		WHERE channel = ? AND merchant_id = ? AND subject_index = ? LIMIT 1`,
		bootstrapChannel, bootstrapMerchantID, index).Scan(&existingID, &deletedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		ciphertext, encErr := protector.Encrypt(strconv.FormatInt(accountID, 10), subject)
		if encErr != nil {
			return false, fmt.Errorf("bootstrap: encrypt subject: %w", encErr)
		}
		if _, execErr := tx.ExecContext(ctx, `INSERT INTO identity_account
			(id, channel, subject_index, subject_ciphertext, display_name, status,
			 token_version, version, merchant_id, password_updated_at, locked_until,
			 must_change_password, created_at, updated_at, deleted_at)
			VALUES (?, ?, ?, ?, '', 'active', 1, 1, ?, ?, NULL, 1, ?, ?, NULL)`,
			accountID, bootstrapChannel, index, ciphertext, bootstrapMerchantID, now, now, now,
		); execErr != nil {
			return false, fmt.Errorf("bootstrap: insert account: %w", execErr)
		}
		return true, nil
	case err != nil:
		return false, fmt.Errorf("bootstrap: query account: %w", err)
	case deletedAt.Valid:
		return false, fmt.Errorf("bootstrap: subject is already owned by a deleted account %d", existingID)
	default:
		return false, nil
	}
}

// findAccountID 返回已存在账号的主键。
func findAccountID(ctx context.Context, tx *sql.Tx, index string) (int64, error) {
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM identity_account
		WHERE channel = ? AND merchant_id = ? AND subject_index = ? LIMIT 1`,
		bootstrapChannel, bootstrapMerchantID, index).Scan(&id); err != nil {
		return 0, fmt.Errorf("bootstrap: query account id: %w", err)
	}
	return id, nil
}

// ensureCredential 仅在账号尚无密码凭据时写入固定口令哈希。
func ensureCredential(ctx context.Context, tx *sql.Tx, accountID int64, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO identity_credential
		(id, account_id, credential_type, credential_hash, algorithm, created_at, updated_at)
		SELECT ?, ?, 'password', ?, 'argon2id', ?, ?
		WHERE NOT EXISTS (
			SELECT 1 FROM identity_credential c
			WHERE c.account_id = ? AND c.credential_type = 'password')`,
		bootstrapCredentialID, accountID, bootstrapPasswordHash, now, now, accountID,
	); err != nil {
		return fmt.Errorf("bootstrap: insert credential: %w", err)
	}
	return nil
}

// findSystemAdminRole 查找种子迁移创建的系统管理员角色。
func findSystemAdminRole(ctx context.Context, tx *sql.Tx) (int64, error) {
	var roleID int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM iam_role
		WHERE channel = ? AND merchant_id = ? AND code = 'system_admin' AND deleted_at IS NULL
		LIMIT 1`, bootstrapChannel, bootstrapMerchantID).Scan(&roleID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errors.New("bootstrap: system_admin role is missing; apply migrations first")
	}
	if err != nil {
		return 0, fmt.Errorf("bootstrap: query system_admin role: %w", err)
	}
	return roleID, nil
}

// ensureBinding 在绑定不存在时创建；已有生效绑定则跳过。
func ensureBinding(ctx context.Context, tx *sql.Tx, accountID, roleID int64, now time.Time) error {
	subjectID := strconv.FormatInt(accountID, 10)
	if _, err := tx.ExecContext(ctx, `INSERT INTO iam_role_binding
		(id, channel, subject_type, subject_id, merchant_id, role_id,
		 expires_at, granted_by, created_at, revoked_at, revoked_by, revoke_reason)
		SELECT ?, ?, 'staff', ?, ?, ?, NULL, ?, ?, NULL, '', ''
		WHERE NOT EXISTS (
			SELECT 1 FROM iam_role_binding b
			WHERE b.channel = ? AND b.subject_type = 'staff' AND b.subject_id = ?
			  AND b.merchant_id = ? AND b.role_id = ? AND b.revoked_at IS NULL)`,
		bootstrapBindingID, bootstrapChannel, subjectID, bootstrapMerchantID, roleID,
		bootstrapActorID, now,
		bootstrapChannel, subjectID, bootstrapMerchantID, roleID,
	); err != nil {
		return fmt.Errorf("bootstrap: insert role binding: %w", err)
	}
	return nil
}

// writeAudit 只在本次新建账号时写入 Outbox 审计，且载荷不含明文主体。
func writeAudit(ctx context.Context, tx *sql.Tx, accountID int64, index, service string, now time.Time) error {
	payload, err := json.Marshal(map[string]any{
		"event_id":      bootstrapEventID,
		"action":        "auth.account.created",
		"actor_id":      bootstrapActorID,
		"actor_type":    "system",
		"channel":       bootstrapChannel,
		"resource_type": "account",
		"resource_id":   strconv.FormatInt(accountID, 10),
		"outcome":       "success",
		"occurred_at":   now.Format("2006-01-02T15:04:05.000Z"),
		"trace_id":      auditTraceID,
		"service":       service,
		"attrs": map[string]any{
			"subject_index":        index,
			"must_change_password": true,
		},
	})
	if err != nil {
		return fmt.Errorf("bootstrap: encode audit payload: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO async_outbox
		(id, topic, event_type, version, tenant_id, aggregate_type, aggregate_id,
		 idempotency_key, trace_id, payload, status, attempts, next_retry_at, last_error,
		 occurred_at, created_at, updated_at)
		VALUES (?, ?, ?, 'v1', '', 'account', ?, '', ?, ?, 'pending', 0, NULL, '',
		        ?, ?, ?)`,
		bootstrapEventID, auditTopic, auditEventType, strconv.FormatInt(accountID, 10),
		auditTraceID, payload, now, now, now,
	); err != nil {
		return fmt.Errorf("bootstrap: insert audit event: %w", err)
	}
	return nil
}
