package persistence

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// menuSelectColumns 是菜单表的显式列清单（禁止 SELECT *，DATA-04）。
const menuSelectColumns = `id, channel, code, parent_code, node_type, route_name, route_path,
	component, redirect, title, title_key, icon, permission_code, sort, hide_in_menu,
	keep_alive, external_url, status, version, updated_by, created_at, updated_at`

// SQLMenuRepository 是 MySQL 8.0 的菜单仓储实现。
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
		&menu.Version, &menu.UpdatedBy, &menu.CreatedAt, &menu.UpdatedAt,
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

// Get 按主键加载菜单；不存在时返回 40400。
func (r *SQLMenuRepository) Get(ctx context.Context, id string) (*domain.Menu, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam menu repository is not configured")
	}
	menuID, err := parseID(id)
	if err != nil {
		return nil, err
	}
	row := resolveExecutor(ctx, r.sql).QueryRowContext(ctx,
		`SELECT `+menuSelectColumns+` FROM iam_menu WHERE id = ?`, menuID)
	menu, err := scanMenu(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("menu not found")
	}
	if err != nil {
		return nil, err
	}
	return menu, nil
}

// Create 写入新菜单；编码 / 路由名冲突或父节点悬空映射为 40900。
func (r *SQLMenuRepository) Create(ctx context.Context, menu *domain.Menu) error {
	if r.sql == nil {
		return apperr.Unavailable("iam menu repository is not configured")
	}
	if menu == nil {
		return apperr.BadRequest("menu is required")
	}
	menuID, err := parseID(menu.ID)
	if err != nil {
		return err
	}
	_, err = resolveExecutor(ctx, r.sql).ExecContext(ctx, `INSERT INTO iam_menu
		(id, channel, code, parent_code, node_type, route_name, route_path, component, redirect,
		 title, title_key, icon, permission_code, sort, hide_in_menu, keep_alive, external_url,
		 status, version, updated_by, created_at, updated_at)
		VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?,
			?, ?, ?, ?, ?)`,
		menuID, string(menu.Channel), menu.Code, menu.ParentCode, string(menu.NodeType),
		menu.RouteName, menu.RoutePath, menu.Component, menu.Redirect,
		menu.Title, menu.TitleKey, menu.Icon, string(menu.PermissionCode), menu.Sort,
		menu.HideInMenu, menu.KeepAlive, menu.ExternalURL, string(menu.Status), menu.Version,
		menu.UpdatedBy, menu.CreatedAt.UTC(), menu.UpdatedAt.UTC())
	if err := conflictOnDuplicate(err, "menu code or route name already exists in this channel"); err != nil {
		return err
	}
	return conflictOnReferencedRow(err, "menu parent or permission code does not exist")
}

// Update 以乐观锁保存菜单；version 不匹配返回 40900。
func (r *SQLMenuRepository) Update(ctx context.Context, menu *domain.Menu, expectedVersion int64) error {
	if r.sql == nil {
		return apperr.Unavailable("iam menu repository is not configured")
	}
	if menu == nil {
		return apperr.BadRequest("menu is required")
	}
	menuID, err := parseID(menu.ID)
	if err != nil {
		return err
	}
	result, err := resolveExecutor(ctx, r.sql).ExecContext(ctx, `UPDATE iam_menu
		SET parent_code = NULLIF(?, ''), node_type = ?, route_name = ?, route_path = ?,
		    component = ?, redirect = ?, title = ?, title_key = ?, icon = ?,
		    permission_code = NULLIF(?, ''), sort = ?, hide_in_menu = ?, keep_alive = ?,
		    external_url = ?, status = ?, version = ?, updated_by = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		menu.ParentCode, string(menu.NodeType), menu.RouteName, menu.RoutePath,
		menu.Component, menu.Redirect, menu.Title, menu.TitleKey, menu.Icon,
		string(menu.PermissionCode), menu.Sort, menu.HideInMenu, menu.KeepAlive,
		menu.ExternalURL, string(menu.Status), menu.Version, menu.UpdatedBy,
		menu.UpdatedAt.UTC(), menuID, expectedVersion)
	if err := conflictOnDuplicate(err, "menu code or route name already exists in this channel"); err != nil {
		return err
	}
	if err := conflictOnReferencedRow(err, "menu parent or permission code does not exist"); err != nil {
		return err
	}
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return apperr.Conflict("menu version conflict")
	}
	return nil
}
