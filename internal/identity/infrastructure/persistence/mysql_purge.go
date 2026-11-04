package persistence

// ENT-15 商户数据硬删除边界（identity 域，D-05 级联任务）。
//
// 删除范围：tenant 终止后按 merchant_id 清理 B 端账号及其从属数据——
// 凭据 / 会话 / 会话族 / MFA / OAuth 关联 / 登录尝试。账号带软删列，
// 处置是物理删除（软删行同样清除——PII 密文与确定性索引不得残留）。
// 审计事件流（identity_audit_event）与会话归档不在本边界（保留期独立）。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// MySQLMerchantPurger 清理 identity 域的商户级数据。
type MySQLMerchantPurger struct {
	db *sql.DB
}

// NewMySQLMerchantPurger 构造；空句柄拒绝。
func NewMySQLMerchantPurger(sqlDB *sql.DB) (*MySQLMerchantPurger, error) {
	if sqlDB == nil {
		return nil, errors.New("identity merchant purger requires a database handle")
	}
	return &MySQLMerchantPurger{db: sqlDB}, nil
}

// Name 边界名。
func (p *MySQLMerchantPurger) Name() string { return "identity" }

// Purge 删除该商户的 identity 数据，返回删除行数。幂等：重跑只删剩余行。
//
// 顺序：先按 account join 清子表（各子表外键 RESTRICT 且无 merchant_id 列），
// 再删账号主行。会话在停用时已撤销（D-02=①），此处只做物理清除。
func (p *MySQLMerchantPurger) Purge(ctx context.Context, merchantID string, _ int) (int64, error) {
	id := strings.TrimSpace(merchantID)
	if id == "" {
		return 0, errors.New("identity: purge requires a merchant id")
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("identity: begin purge tx %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	// 子表经 account join 清理；account 主行最后删（含软删行——PII 不得残留）。
	statements := []struct{ name, query string }{
		{"identity_credential", `DELETE c FROM identity_credential c
			 JOIN identity_account a ON c.account_id = a.id
			 WHERE a.merchant_id = ? AND a.merchant_id != ''`},
		{"identity_session", `DELETE s FROM identity_session s
			 JOIN identity_account a ON s.account_id = a.id
			 WHERE a.merchant_id = ? AND a.merchant_id != ''`},
		{"identity_session_family", `DELETE f FROM identity_session_family f
			 JOIN identity_account a ON f.account_id = a.id
			 WHERE a.merchant_id = ? AND a.merchant_id != ''`},
		{"identity_mfa", `DELETE m FROM identity_mfa m
			 JOIN identity_account a ON m.account_id = a.id
			 WHERE a.merchant_id = ? AND a.merchant_id != ''`},
		{"identity_oauth_identity", `DELETE o FROM identity_oauth_identity o
			 JOIN identity_account a ON o.account_id = a.id
			 WHERE a.merchant_id = ? AND a.merchant_id != ''`},
		{"identity_login_attempt", `DELETE l FROM identity_login_attempt l
			 JOIN identity_account a ON l.account_id = a.id
			 WHERE a.merchant_id = ? AND a.merchant_id != ''`},
		{"identity_account", `DELETE FROM identity_account WHERE merchant_id = ? AND merchant_id != ''`},
	}
	var total int64
	for _, stmt := range statements {
		res, err := tx.ExecContext(ctx, stmt.query, id)
		if err != nil {
			return 0, fmt.Errorf("identity: purge %s %s: %w", stmt.name, id, err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("identity: purge %s %s affected: %w", stmt.name, id, err)
		}
		total += affected
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("identity: commit purge %s: %w", id, err)
	}
	return total, nil
}
