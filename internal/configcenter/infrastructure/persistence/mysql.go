// Package persistence 是配置中心 M1 的 MySQL 持久化实现。
//
// 表结构见 migrations/000031_create_config_center.up.sql，列与唯一键冻结自
// docs/config-center-plan.md 附录 A。语义与 infrastructure/memory 保持一致：
// 唯一键冲突返回 domain.ErrDuplicateKey，乐观锁返回 domain.ErrVersionConflict，
// 发布迁移前置状态不符返回 domain.ErrStateConflict，指针 CAS 失败返回
// domain.ErrPointerConflict。
//
// 事务：仓储在「调用方已开启事务」时复用同一 *sql.Tx（pkg/db.TxFromContext）；
// 发布快照（头 + 条目）作为原子写入单元，未处于事务中时由仓储自行开启。
//
// 三个端口各自独立类型而非单一聚合：ItemRepository / ReleaseRepository /
// PointerRepository 存在同名方法（如 Get / Create），Go 不支持方法重载。
package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// Repositories 汇总三个持久化端口的 MySQL 实现。
type Repositories struct {
	Items    domain.ItemRepository
	Releases domain.ReleaseRepository
	Pointers domain.PointerRepository
}

// New 构造共享同一连接池的仓储集合。
func New(sqlDB *sql.DB) (*Repositories, error) {
	items, err := NewItemRepository(sqlDB)
	if err != nil {
		return nil, err
	}
	releases, err := NewReleaseRepository(sqlDB)
	if err != nil {
		return nil, err
	}
	pointers, err := NewPointerRepository(sqlDB)
	if err != nil {
		return nil, err
	}
	return &Repositories{Items: items, Releases: releases, Pointers: pointers}, nil
}

// execer / queryer 让仓储在事务内复用调用方的 *sql.Tx。
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func execFrom(ctx context.Context, sqlDB *sql.DB) execer {
	if tx, ok := db.TxFromContext(ctx); ok {
		return tx
	}
	return sqlDB
}

func queryFrom(ctx context.Context, sqlDB *sql.DB) queryer {
	if tx, ok := db.TxFromContext(ctx); ok {
		return tx
	}
	return sqlDB
}

// ---------------------------------------------------------------- item

// ItemRepository 是 domain.ItemRepository 的 MySQL 实现。
type ItemRepository struct {
	db *sql.DB
}

var _ domain.ItemRepository = (*ItemRepository)(nil)

// NewItemRepository 构造配置键仓储；db 为 nil 返回错误。
func NewItemRepository(sqlDB *sql.DB) (*ItemRepository, error) {
	if sqlDB == nil {
		return nil, errors.New("configcenter item repository requires a database handle")
	}
	return &ItemRepository{db: sqlDB}, nil
}

// itemColumns 与 scanItem 一一对齐。
//
// sensitive 是 MySQL 8.0 保留字，所有引用处必须加反引号，否则建表与查询都会报 1064。
const itemColumns = "id, env, namespace, `key`, value_type, description, owner, status, " +
	"current_version, version, `sensitive`, validation_json, expires_at, " +
	"created_by, updated_by, created_at, updated_at"

// Create 写入配置键元数据；ID 或 (env, namespace, key) 冲突返回 ErrDuplicateKey。
func (r *ItemRepository) Create(ctx context.Context, item domain.Item) error {
	_, err := execFrom(ctx, r.db).ExecContext(ctx,
		`INSERT INTO config_item (`+itemColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.Env, item.Namespace, item.Key, string(item.ValueType),
		item.Description, item.Owner, string(item.Status), item.CurrentVersion,
		item.Version, item.Sensitive, nullJSON(item.ValidationJSON),
		nullTimePtr(item.ExpiresAt), item.CreatedBy, item.UpdatedBy,
		item.CreatedAt.UTC(), item.UpdatedAt.UTC())
	if err != nil {
		if isDuplicateEntry(err) {
			return domain.ErrDuplicateKey
		}
		return fmt.Errorf("configcenter: create item: %w", err)
	}
	return nil
}

// GetByID 按 ID 读取；不存在返回 ErrNotFound。
func (r *ItemRepository) GetByID(ctx context.Context, id int64) (domain.Item, error) {
	row := queryFrom(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+itemColumns+` FROM config_item WHERE id = ?`, id)
	item, err := scanItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Item{}, domain.ErrNotFound
		}
		return domain.Item{}, err
	}
	return item, nil
}

// GetByKey 按 (env, namespace, key) 读取；不存在返回 ErrNotFound。
func (r *ItemRepository) GetByKey(ctx context.Context, env, namespace, key string) (domain.Item, error) {
	row := queryFrom(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+itemColumns+` FROM config_item WHERE env = ? AND namespace = ? AND `+"`key`"+` = ?`,
		env, namespace, key)
	item, err := scanItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Item{}, domain.ErrNotFound
		}
		return domain.Item{}, err
	}
	return item, nil
}

// List 按状态过滤列出配置键（key 字典序，稳定分页）；status 为空表示全部。
func (r *ItemRepository) List(ctx context.Context, env, namespace string, status domain.ItemStatus, limit, offset int) ([]domain.Item, int, error) {
	where := " WHERE env = ? AND namespace = ?"
	args := []any{env, namespace}
	if status != "" {
		where += " AND status = ?"
		args = append(args, string(status))
	}
	var total int
	if err := queryFrom(ctx, r.db).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM config_item`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("configcenter: count items: %w", err)
	}
	limit, offset = normalizePage(limit, offset)
	rows, err := queryFrom(ctx, r.db).QueryContext(ctx,
		`SELECT `+itemColumns+` FROM config_item`+where+
			` ORDER BY `+"`key`"+` LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("configcenter: list items: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]domain.Item, 0, limit)
	for rows.Next() {
		item, scanErr := scanItem(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// UpdateCAS 以 expectedVersion 做乐观锁更新；版本不匹配返回 ErrVersionConflict。
func (r *ItemRepository) UpdateCAS(ctx context.Context, item domain.Item, expectedVersion int64) error {
	res, err := execFrom(ctx, r.db).ExecContext(ctx,
		`UPDATE config_item SET env = ?, namespace = ?, `+"`key`"+` = ?, value_type = ?,
		 description = ?, owner = ?, status = ?, current_version = ?, version = ?,
		 `+"`sensitive`"+` = ?, validation_json = ?, expires_at = ?, updated_by = ?, updated_at = ?
		 WHERE id = ? AND version = ?`,
		item.Env, item.Namespace, item.Key, string(item.ValueType),
		item.Description, item.Owner, string(item.Status), item.CurrentVersion,
		item.Version, item.Sensitive, nullJSON(item.ValidationJSON),
		nullTimePtr(item.ExpiresAt), item.UpdatedBy, item.UpdatedAt.UTC(),
		item.ID, expectedVersion)
	if err != nil {
		if isDuplicateEntry(err) {
			return domain.ErrDuplicateKey
		}
		return fmt.Errorf("configcenter: update item: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("configcenter: update item affected: %w", err)
	}
	if affected == 0 {
		if _, err := r.GetByID(ctx, item.ID); err != nil {
			return err
		}
		return domain.ErrVersionConflict
	}
	return nil
}

// AppendVersion 追加不可变版本；(item_id, version) 冲突返回 ErrDuplicateKey。
func (r *ItemRepository) AppendVersion(ctx context.Context, version domain.ItemVersion) error {
	var exists int
	err := queryFrom(ctx, r.db).QueryRowContext(ctx,
		`SELECT 1 FROM config_item WHERE id = ?`, version.ItemID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("configcenter: check item for version: %w", err)
	}
	_, err = execFrom(ctx, r.db).ExecContext(ctx,
		`INSERT INTO config_item_version (id, item_id, version, value_text, value_hash, change_summary, created_by, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		version.ID, version.ItemID, version.Version, version.ValueText,
		version.ValueHash, version.ChangeSummary, version.CreatedBy,
		version.CreatedAt.UTC())
	if err != nil {
		if isDuplicateEntry(err) {
			return domain.ErrDuplicateKey
		}
		return fmt.Errorf("configcenter: append item version: %w", err)
	}
	return nil
}

// GetVersion 读取指定版本；不存在返回 ErrNotFound。
func (r *ItemRepository) GetVersion(ctx context.Context, itemID, version int64) (domain.ItemVersion, error) {
	row := queryFrom(ctx, r.db).QueryRowContext(ctx,
		`SELECT id, item_id, version, value_text, value_hash, change_summary, created_by, created_at
		 FROM config_item_version WHERE item_id = ? AND version = ?`, itemID, version)
	item, err := scanItemVersion(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ItemVersion{}, domain.ErrNotFound
		}
		return domain.ItemVersion{}, err
	}
	return item, nil
}

// ListVersions 按版本升序列出并稳定分页。
func (r *ItemRepository) ListVersions(ctx context.Context, itemID int64, limit, offset int) ([]domain.ItemVersion, int, error) {
	var total int
	if err := queryFrom(ctx, r.db).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM config_item_version WHERE item_id = ?`, itemID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("configcenter: count item versions: %w", err)
	}
	limit, offset = normalizePage(limit, offset)
	rows, err := queryFrom(ctx, r.db).QueryContext(ctx,
		`SELECT id, item_id, version, value_text, value_hash, change_summary, created_by, created_at
		 FROM config_item_version WHERE item_id = ? ORDER BY version LIMIT ? OFFSET ?`,
		itemID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("configcenter: list item versions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	versions := make([]domain.ItemVersion, 0, limit)
	for rows.Next() {
		version, scanErr := scanItemVersion(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		versions = append(versions, version)
	}
	return versions, total, rows.Err()
}

// ---------------------------------------------------------------- release

// ReleaseRepository 是 domain.ReleaseRepository 的 MySQL 实现。
type ReleaseRepository struct {
	db *sql.DB
}

var _ domain.ReleaseRepository = (*ReleaseRepository)(nil)

// NewReleaseRepository 构造发布仓储；db 为 nil 返回错误。
func NewReleaseRepository(sqlDB *sql.DB) (*ReleaseRepository, error) {
	if sqlDB == nil {
		return nil, errors.New("configcenter release repository requires a database handle")
	}
	return &ReleaseRepository{db: sqlDB}, nil
}

// releaseColumns 与 scanRelease 一一对齐（ADR-0016 起无签名列）。
const releaseColumns = "id, env, namespace, release_no, name, description, status, strategy, " +
	"target_rule_json, snapshot_hash, snapshot_size, " +
	"based_on_release_id, created_by, submitted_by, approved_by, approved_at, published_at, " +
	"created_at, updated_at"

// Create 在同一事务内写入发布头与快照条目；ID 或 release_no 冲突返回 ErrDuplicateKey。
func (r *ReleaseRepository) Create(ctx context.Context, release domain.Release, items []domain.ReleaseItem) error {
	return db.WithinUnitOfWork(ctx, r.db, nil, func(ctx context.Context) error {
		_, err := execFrom(ctx, r.db).ExecContext(ctx,
			`INSERT INTO config_release (`+releaseColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			release.ID, release.Env, release.Namespace, release.ReleaseNo, release.Name,
			release.Description, string(release.Status), string(release.Strategy),
			nullJSON(release.TargetRuleJSON), release.SnapshotHash, release.SnapshotSize,
			nullInt64(release.BasedOnReleaseID),
			release.CreatedBy, nullString(release.SubmittedBy), nullString(release.ApprovedBy),
			nullTimePtr(release.ApprovedAt), nullTimePtr(release.PublishedAt),
			release.CreatedAt.UTC(), release.UpdatedAt.UTC())
		if err != nil {
			if isDuplicateEntry(err) {
				return domain.ErrDuplicateKey
			}
			return fmt.Errorf("configcenter: create release: %w", err)
		}
		for _, item := range items {
			if item.ReleaseID != release.ID {
				return domain.ErrStateConflict
			}
			if _, err := execFrom(ctx, r.db).ExecContext(ctx,
				`INSERT INTO config_release_item (release_id, item_id, item_version, config_key, value_hash)
				 VALUES (?, ?, ?, ?, ?)`,
				item.ReleaseID, item.ItemID, item.ItemVersion, item.ConfigKey, item.ValueHash); err != nil {
				if isDuplicateEntry(err) {
					return domain.ErrDuplicateKey
				}
				return fmt.Errorf("configcenter: create release item: %w", err)
			}
		}
		return nil
	})
}

// Get 按 ID 读取发布；不存在返回 ErrNotFound。
func (r *ReleaseRepository) Get(ctx context.Context, id int64) (domain.Release, error) {
	row := queryFrom(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+releaseColumns+` FROM config_release WHERE id = ?`, id)
	release, err := scanRelease(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Release{}, domain.ErrNotFound
		}
		return domain.Release{}, err
	}
	return release, nil
}

// GetByNo 按 (env, namespace, release_no) 读取；不存在返回 ErrNotFound。
func (r *ReleaseRepository) GetByNo(ctx context.Context, env, namespace string, releaseNo int64) (domain.Release, error) {
	row := queryFrom(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+releaseColumns+` FROM config_release WHERE env = ? AND namespace = ? AND release_no = ?`,
		env, namespace, releaseNo)
	release, err := scanRelease(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Release{}, domain.ErrNotFound
		}
		return domain.Release{}, err
	}
	return release, nil
}

// List 按状态过滤列出发布（release_no 降序，稳定分页）；status 为空表示全部。
func (r *ReleaseRepository) List(ctx context.Context, filter domain.ReleaseListFilter, limit, offset int) ([]domain.Release, int, error) {
	where := " WHERE env = ? AND namespace = ?"
	args := []any{filter.Env, filter.Namespace}
	if filter.Status != "" {
		where += " AND status = ?"
		args = append(args, string(filter.Status))
	}
	if owner := strings.TrimSpace(filter.Owner); owner != "" {
		where += " AND created_by = ?"
		args = append(args, owner)
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		needle := strings.ToLower(keyword)
		where += " AND (INSTR(LOWER(name), ?) > 0 OR INSTR(LOWER(description), ?) > 0)"
		args = append(args, needle, needle)
	}
	var total int
	if err := queryFrom(ctx, r.db).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM config_release`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("configcenter: count releases: %w", err)
	}
	limit, offset = normalizePage(limit, offset)
	rows, err := queryFrom(ctx, r.db).QueryContext(ctx,
		`SELECT `+releaseColumns+` FROM config_release`+where+
			` ORDER BY release_no DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("configcenter: list releases: %w", err)
	}
	defer func() { _ = rows.Close() }()
	releases := make([]domain.Release, 0, limit)
	for rows.Next() {
		release, scanErr := scanRelease(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		releases = append(releases, release)
	}
	return releases, total, rows.Err()
}

// NextReleaseNo 返回同 (env, namespace) 的下一个候选发布号。
//
// 只提供候选值；并发下由唯一键 uk_config_release_no 兜底（冲突返回 ErrDuplicateKey）。
func (r *ReleaseRepository) NextReleaseNo(ctx context.Context, env, namespace string) (int64, error) {
	var maxNo int64
	if err := queryFrom(ctx, r.db).QueryRowContext(ctx,
		`SELECT COALESCE(MAX(release_no), 0) FROM config_release WHERE env = ? AND namespace = ?`,
		env, namespace).Scan(&maxNo); err != nil {
		return 0, fmt.Errorf("configcenter: next release no: %w", err)
	}
	return maxNo + 1, nil
}

// Transition 按前置状态迁移发布单；前置状态不符返回 ErrStateConflict。
func (r *ReleaseRepository) Transition(ctx context.Context, id int64, from, to domain.ReleaseStatus, actor string, at time.Time) error {
	if !from.CanTransitionTo(to) {
		return domain.ErrStateConflict
	}
	sets := []string{"status = ?", "updated_at = ?"}
	args := []any{string(to), at.UTC()}
	switch to {
	case domain.ReleaseStatusPendingReview:
		sets = append(sets, "submitted_by = ?")
		args = append(args, actor)
	case domain.ReleaseStatusApproved:
		sets = append(sets, "approved_by = ?", "approved_at = ?")
		args = append(args, actor, at.UTC())
	case domain.ReleaseStatusActive:
		sets = append(sets, "published_at = ?")
		args = append(args, at.UTC())
	}
	args = append(args, id, string(from))
	res, err := execFrom(ctx, r.db).ExecContext(ctx,
		`UPDATE config_release SET `+strings.Join(sets, ", ")+` WHERE id = ? AND status = ?`,
		args...)
	if err != nil {
		return fmt.Errorf("configcenter: transition release: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("configcenter: transition release affected: %w", err)
	}
	if affected == 0 {
		if _, err := r.Get(ctx, id); err != nil {
			return err
		}
		return domain.ErrStateConflict
	}
	return nil
}

// UpdateSnapshotSignature 已废弃（ADR-0016）；仅校验发布存在。
func (r *ReleaseRepository) UpdateSnapshotSignature(ctx context.Context, id int64, _, _ string, _ time.Time) error {
	if _, err := r.Get(ctx, id); err != nil {
		return err
	}
	return nil
}

// ListItems 返回发布快照条目（按 item_id 升序）。
func (r *ReleaseRepository) ListItems(ctx context.Context, releaseID int64) ([]domain.ReleaseItem, error) {
	rows, err := queryFrom(ctx, r.db).QueryContext(ctx,
		`SELECT release_id, item_id, item_version, config_key, value_hash
		 FROM config_release_item WHERE release_id = ? ORDER BY item_id`, releaseID)
	if err != nil {
		return nil, fmt.Errorf("configcenter: list release items: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]domain.ReleaseItem, 0, 8)
	for rows.Next() {
		var item domain.ReleaseItem
		if err := rows.Scan(&item.ReleaseID, &item.ItemID, &item.ItemVersion,
			&item.ConfigKey, &item.ValueHash); err != nil {
			return nil, fmt.Errorf("configcenter: scan release item: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// AppendReview 追加审批记录；ID 冲突返回 ErrDuplicateKey。
func (r *ReleaseRepository) AppendReview(ctx context.Context, review domain.ReleaseReview) error {
	_, err := execFrom(ctx, r.db).ExecContext(ctx,
		`INSERT INTO config_release_review (id, release_id, reviewer, decision, comment, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		review.ID, review.ReleaseID, review.Reviewer, string(review.Decision),
		review.Comment, review.CreatedAt.UTC())
	if err != nil {
		if isDuplicateEntry(err) {
			return domain.ErrDuplicateKey
		}
		return fmt.Errorf("configcenter: append review: %w", err)
	}
	return nil
}

// ListReviews 返回审批记录（按 created_at、id 升序）。
func (r *ReleaseRepository) ListReviews(ctx context.Context, releaseID int64) ([]domain.ReleaseReview, error) {
	rows, err := queryFrom(ctx, r.db).QueryContext(ctx,
		`SELECT id, release_id, reviewer, decision, comment, created_at
		 FROM config_release_review WHERE release_id = ? ORDER BY created_at, id`, releaseID)
	if err != nil {
		return nil, fmt.Errorf("configcenter: list reviews: %w", err)
	}
	defer func() { _ = rows.Close() }()
	reviews := make([]domain.ReleaseReview, 0, 4)
	for rows.Next() {
		var review domain.ReleaseReview
		var decision string
		if err := rows.Scan(&review.ID, &review.ReleaseID, &review.Reviewer,
			&decision, &review.Comment, &review.CreatedAt); err != nil {
			return nil, fmt.Errorf("configcenter: scan review: %w", err)
		}
		review.Decision = domain.ReviewDecision(decision)
		reviews = append(reviews, review)
	}
	return reviews, rows.Err()
}

// ---------------------------------------------------------------- pointer

// PointerRepository 是 domain.PointerRepository 的 MySQL 实现。
type PointerRepository struct {
	db *sql.DB
}

var _ domain.PointerRepository = (*PointerRepository)(nil)

// NewPointerRepository 构造指针仓储；db 为 nil 返回错误。
func NewPointerRepository(sqlDB *sql.DB) (*PointerRepository, error) {
	if sqlDB == nil {
		return nil, errors.New("configcenter pointer repository requires a database handle")
	}
	return &PointerRepository{db: sqlDB}, nil
}

// Get 读取生效指针；不存在返回 ErrNotFound。
func (r *PointerRepository) Get(ctx context.Context, env, namespace string) (domain.Pointer, error) {
	row := queryFrom(ctx, r.db).QueryRowContext(ctx,
		`SELECT env, namespace, active_release_id, version, updated_at
		 FROM config_release_pointer WHERE env = ? AND namespace = ?`, env, namespace)
	var pointer domain.Pointer
	if err := row.Scan(&pointer.Env, &pointer.Namespace, &pointer.ActiveReleaseID,
		&pointer.Version, &pointer.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Pointer{}, domain.ErrNotFound
		}
		return domain.Pointer{}, fmt.Errorf("configcenter: get pointer: %w", err)
	}
	return pointer, nil
}

// ListByEnv 返回某环境下全部生效指针。
func (r *PointerRepository) ListByEnv(ctx context.Context, env string) ([]domain.Pointer, error) {
	rows, err := queryFrom(ctx, r.db).QueryContext(ctx,
		`SELECT env, namespace, active_release_id, version, updated_at
		 FROM config_release_pointer WHERE env = ? ORDER BY namespace`, env)
	if err != nil {
		return nil, fmt.Errorf("configcenter: list pointers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]domain.Pointer, 0, 8)
	for rows.Next() {
		var pointer domain.Pointer
		if err := rows.Scan(&pointer.Env, &pointer.Namespace, &pointer.ActiveReleaseID,
			&pointer.Version, &pointer.UpdatedAt); err != nil {
			return nil, fmt.Errorf("configcenter: scan pointer: %w", err)
		}
		out = append(out, pointer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("configcenter: list pointers rows: %w", err)
	}
	return out, nil
}

// Create 初始化指针；已存在返回 ErrDuplicateKey。
func (r *PointerRepository) Create(ctx context.Context, pointer domain.Pointer) error {
	_, err := execFrom(ctx, r.db).ExecContext(ctx,
		`INSERT INTO config_release_pointer (env, namespace, active_release_id, version, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		pointer.Env, pointer.Namespace, pointer.ActiveReleaseID, pointer.Version,
		pointer.UpdatedAt.UTC())
	if err != nil {
		if isDuplicateEntry(err) {
			return domain.ErrDuplicateKey
		}
		return fmt.Errorf("configcenter: create pointer: %w", err)
	}
	return nil
}

// CompareAndSwap 以 expectedVersion 切换生效发布；失败返回 ErrPointerConflict。
func (r *PointerRepository) CompareAndSwap(ctx context.Context, env, namespace string, expectedVersion, activeReleaseID int64, now time.Time) (domain.Pointer, error) {
	res, err := execFrom(ctx, r.db).ExecContext(ctx,
		`UPDATE config_release_pointer SET active_release_id = ?, version = version + 1, updated_at = ?
		 WHERE env = ? AND namespace = ? AND version = ?`,
		activeReleaseID, now.UTC(), env, namespace, expectedVersion)
	if err != nil {
		return domain.Pointer{}, fmt.Errorf("configcenter: swap pointer: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return domain.Pointer{}, fmt.Errorf("configcenter: swap pointer affected: %w", err)
	}
	if affected == 0 {
		if _, err := r.Get(ctx, env, namespace); err != nil {
			return domain.Pointer{}, err
		}
		return domain.Pointer{}, domain.ErrPointerConflict
	}
	return r.Get(ctx, env, namespace)
}

// ---------------------------------------------------------------- scan helpers

type scanner interface{ Scan(dest ...any) error }

func scanItem(s scanner) (domain.Item, error) {
	var (
		item       domain.Item
		valueType  string
		status     string
		validation sql.NullString
		expiresAt  sql.NullTime
	)
	if err := s.Scan(&item.ID, &item.Env, &item.Namespace, &item.Key, &valueType,
		&item.Description, &item.Owner, &status, &item.CurrentVersion, &item.Version,
		&item.Sensitive, &validation, &expiresAt, &item.CreatedBy, &item.UpdatedBy,
		&item.CreatedAt, &item.UpdatedAt); err != nil {
		return domain.Item{}, err
	}
	item.ValueType = domain.ValueType(valueType)
	item.Status = domain.ItemStatus(status)
	if validation.Valid {
		item.ValidationJSON = validation.String
	}
	if expiresAt.Valid {
		expiresAtTime := expiresAt.Time
		item.ExpiresAt = &expiresAtTime
	}
	return item, nil
}

func scanItemVersion(s scanner) (domain.ItemVersion, error) {
	var version domain.ItemVersion
	if err := s.Scan(&version.ID, &version.ItemID, &version.Version, &version.ValueText,
		&version.ValueHash, &version.ChangeSummary, &version.CreatedBy,
		&version.CreatedAt); err != nil {
		return domain.ItemVersion{}, err
	}
	return version, nil
}

func scanRelease(s scanner) (domain.Release, error) {
	var (
		release     domain.Release
		status      string
		strategy    string
		targetRule  sql.NullString
		basedOn     sql.NullInt64
		submittedBy sql.NullString
		approvedBy  sql.NullString
		approvedAt  sql.NullTime
		publishedAt sql.NullTime
	)
	if err := s.Scan(&release.ID, &release.Env, &release.Namespace, &release.ReleaseNo,
		&release.Name, &release.Description, &status, &strategy, &targetRule,
		&release.SnapshotHash, &release.SnapshotSize, &basedOn, &release.CreatedBy,
		&submittedBy, &approvedBy, &approvedAt, &publishedAt, &release.CreatedAt,
		&release.UpdatedAt); err != nil {
		return domain.Release{}, err
	}
	release.Status = domain.ReleaseStatus(status)
	release.Strategy = domain.ReleaseStrategy(strategy)
	if targetRule.Valid {
		release.TargetRuleJSON = targetRule.String
	}
	if basedOn.Valid {
		basedOnID := basedOn.Int64
		release.BasedOnReleaseID = &basedOnID
	}
	if submittedBy.Valid {
		release.SubmittedBy = submittedBy.String
	}
	if approvedBy.Valid {
		release.ApprovedBy = approvedBy.String
	}
	if approvedAt.Valid {
		approvedAtTime := approvedAt.Time
		release.ApprovedAt = &approvedAtTime
	}
	if publishedAt.Valid {
		publishedAtTime := publishedAt.Time
		release.PublishedAt = &publishedAtTime
	}
	return release, nil
}

// ------------------------------------------------ value helpers

// normalizePage 统一分页默认值与下界（与 memory 实现一致）。
func normalizePage(limit, offset int) (normalizedLimit, normalizedOffset int) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// nullJSON 空串转 NULL（JSON 列不接受空串）。
func nullJSON(raw string) any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return raw
}

// nullString 空串转 NULL（可空文本列）。
func nullString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

// nullInt64 指针转可空值。
func nullInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// nullTimePtr 指针转可空值（DTO 侧使用指针表达可选时间）。
func nullTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

// isDuplicateEntry 判定 MySQL 1062 唯一冲突（自含副本，避免跨域依赖）。
func isDuplicateEntry(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "Error 1062") ||
		strings.Contains(err.Error(), "Duplicate entry")
}
