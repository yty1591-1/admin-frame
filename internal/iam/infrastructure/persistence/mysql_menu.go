package persistence

import (
	"context"
	"database/sql"
	"strings"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// menuSelectColumns 是菜单表的显式列清单（禁止 SELECT *，DATA-04）。
const menuSelectColumns = `id, channel, code, parent_code, node_type, route_name, route_path,
	component, redirect, title, title_key, icon, permission_code, sort, hide_in_menu,
	keep_alive, external_url, status, updated_by, created_at, updated_at`

// SQLMenuRepository 是 MySQL 8.0 的菜单只读仓储实现。
type SQLMenuRepository struct {
	sql *sql.DB
}

// NewSQLMenuRepository 基于已打开的 *sql.DB 创建菜单仓储。
func NewSQLMenuRepository(sqlDB *sql.DB) *SQLMenuRepository {
	return &SQLMenuRepository{sql: sqlDB}
}

// List 按端别与状态返回菜单，按 sort、code 升序保证树构建稳定。
func (r *SQLMenuRepository) List(ctx context.Context, channel domain.Channel, status domain.MenuStatus) ([]*domain.Menu, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam menu repository is not configured")
	}
	var builder strings.Builder
	builder.WriteString(`SELECT ` + menuSelectColumns + ` FROM iam_menu WHERE channel = ?`)
	args := []any{string(channel)}
	if status != "" {
		builder.WriteString(` AND status = ?`)
		args = append(args, string(status))
	}
	builder.WriteString(` ORDER BY sort, code`)

	rows, err := resolveExecutor(ctx, r.sql).QueryContext(ctx, builder.String(), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]*domain.Menu, 0, 16)
	for rows.Next() {
		menu, scanErr := scanMenu(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, menu)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// scanMenu 扫描一行菜单；parent_code / permission_code 允许为 NULL。
func scanMenu(row scanRow) (*domain.Menu, error) {
	var (
		menu           domain.Menu
		parentCode     sql.NullString
		permissionCode sql.NullString
	)
	if err := row.Scan(
		&menu.ID, &menu.Channel, &menu.Code, &parentCode, &menu.NodeType,
		&menu.RouteName, &menu.RoutePath, &menu.Component, &menu.Redirect,
		&menu.Title, &menu.TitleKey, &menu.Icon, &permissionCode, &menu.Sort,
		&menu.HideInMenu, &menu.KeepAlive, &menu.ExternalURL, &menu.Status,
		&menu.UpdatedBy, &menu.CreatedAt, &menu.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if parentCode.Valid {
		menu.ParentCode = parentCode.String
	}
	if permissionCode.Valid {
		menu.PermissionCode = iam.PermissionCode(permissionCode.String)
	}
	menu.CreatedAt = menu.CreatedAt.UTC()
	menu.UpdatedAt = menu.UpdatedAt.UTC()
	return &menu, nil
}
