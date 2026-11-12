package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// ItemService 是配置键管理用例：创建、改值（只追加版本）、治理状态迁移。
type ItemService struct {
	items domain.ItemRepository
	specs *spec.Registry
	audit auditContext
	ids   id.Generator
	now   func() time.Time
}

// NewItemService 构造配置键用例；仓储、key spec 注册表、审计出口与 ID 生成器必填。
func NewItemService(items domain.ItemRepository, specs *spec.Registry,
	emitter audit.Emitter, ids id.Generator, opts Options) (*ItemService, error) {
	switch {
	case items == nil:
		return nil, apperr.Internal("config item service requires a repository", nil)
	case specs == nil:
		return nil, apperr.Internal("config item service requires a key spec registry", nil)
	case emitter == nil:
		return nil, apperr.Internal("config item service requires an audit emitter", nil)
	case ids == nil:
		return nil, apperr.Internal("config item service requires an id generator", nil)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &ItemService{
		items: items,
		specs: specs,
		audit: auditContext{emitter: emitter, ids: ids, service: opts.Service, now: now},
		ids:   ids,
		now:   now,
	}, nil
}

// CreateItemInput 是新建配置键入参。
type CreateItemInput struct {
	Env           string
	Namespace     string
	Key           string
	Value         *string
	Description   string
	Owner         string
	Status        domain.ItemStatus
	ExpiresAt     *time.Time
	ChangeSummary string
	Actor         Actor
}

// CreateItem 创建配置键；提供 Value 时同时写入版本 1。
//
// 调用方须在同一事务内调用（Create 与 AppendVersion 需要原子生效），
// 事务由 transport 层通过 pkg/db.WithinUnitOfWork 开启。
func (s *ItemService) CreateItem(ctx context.Context, in CreateItemInput) (domain.Item, error) {
	if err := requireActor(in.Actor); err != nil {
		return domain.Item{}, err
	}
	if !domain.ValidEnv(in.Env) {
		return domain.Item{}, apperr.BadRequest(fmt.Sprintf("invalid env %q", in.Env))
	}
	keySpec, ok := s.specs.Lookup(in.Namespace, in.Key)
	if !ok {
		return domain.Item{}, apperr.BadRequest(fmt.Sprintf("key %q is not declared in code key spec", in.Key))
	}
	if in.Value != nil && keySpec.Sensitive {
		return domain.Item{}, apperr.BadRequest("sensitive key values are managed by config_secret (M2)")
	}
	if in.Value != nil {
		if err := keySpec.ValidateValue(*in.Value); err != nil {
			return domain.Item{}, apperr.BadRequest(err.Error())
		}
	}

	status := in.Status
	if status == "" {
		status = domain.ItemStatusDraft
	}
	if !status.Valid() {
		return domain.Item{}, apperr.BadRequest(fmt.Sprintf("invalid item status %q", status))
	}

	itemID, err := s.ids.Next()
	if err != nil {
		return domain.Item{}, apperr.Internal("generate item id failed", err)
	}
	now := s.now().UTC()
	item := domain.Item{
		ID:             itemID,
		Env:            in.Env,
		Namespace:      in.Namespace,
		Key:            in.Key,
		ValueType:      keySpec.ValueType,
		Description:    defaultString(in.Description, keySpec.Description),
		Owner:          defaultString(in.Owner, keySpec.Owner),
		Status:         status,
		CurrentVersion: 0,
		Version:        1,
		Sensitive:      keySpec.Sensitive,
		ValidationJSON: keySpec.ValidationJSON(),
		ExpiresAt:      in.ExpiresAt,
		CreatedBy:      in.Actor.SubjectID,
		UpdatedBy:      in.Actor.SubjectID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if in.Value != nil {
		item.CurrentVersion = 1
	}
	if err := item.Validate(); err != nil {
		return domain.Item{}, apperr.BadRequest(err.Error())
	}

	if err := s.items.Create(ctx, item); err != nil {
		return domain.Item{}, translate(err)
	}
	attrs := map[string]string{
		"env":             item.Env,
		"namespace":       item.Namespace,
		"key":             item.Key,
		"value_type":      string(item.ValueType),
		"status":          string(item.Status),
		"current_version": fmt.Sprintf("%d", item.CurrentVersion),
	}
	if in.Value != nil {
		version, err := s.buildVersion(item, 1, *in.Value, in.ChangeSummary, in.Actor.SubjectID, now)
		if err != nil {
			return domain.Item{}, err
		}
		if err := s.items.AppendVersion(ctx, version); err != nil {
			return domain.Item{}, translate(err)
		}
		attrs["value_hash"] = version.ValueHash
	}

	if err := s.audit.emit(ctx, in.Actor, audit.ActionConfigChange, ResourceTypeItem,
		itemResourceID(item), audit.OutcomeSuccess, "create", attrs); err != nil {
		return domain.Item{}, apperr.Internal("emit config audit failed", err)
	}
	return item, nil
}

// UpdateValueInput 是改值入参；值不可原地修改，只能追加新版本。
type UpdateValueInput struct {
	Env             string
	Namespace       string
	Key             string
	Value           string
	ChangeSummary   string
	ExpectedVersion *int64
	Actor           Actor
}

// UpdateValue 追加新版本并推进 current_version。
//
// 写入顺序：先 AppendVersion 再 CAS 元数据；调用方须包事务，否则 CAS 失败
// 会留下未引用的版本行。
func (s *ItemService) UpdateValue(ctx context.Context, in UpdateValueInput) (domain.Item, error) {
	if err := requireActor(in.Actor); err != nil {
		return domain.Item{}, err
	}
	item, err := s.get(ctx, in.Env, in.Namespace, in.Key)
	if err != nil {
		return domain.Item{}, err
	}
	keySpec, ok := s.specs.Lookup(item.Namespace, item.Key)
	if !ok {
		return domain.Item{}, apperr.BadRequest(fmt.Sprintf("key %q is not declared in code key spec", item.Key))
	}
	if item.Sensitive || keySpec.Sensitive {
		return domain.Item{}, apperr.BadRequest("sensitive key values are managed by config_secret (M2)")
	}
	if item.Status == domain.ItemStatusArchived {
		return domain.Item{}, apperr.Conflict("archived item is terminal and cannot accept new values")
	}
	if err := keySpec.ValidateValue(in.Value); err != nil {
		return domain.Item{}, apperr.BadRequest(err.Error())
	}

	now := s.now().UTC()
	nextVersion := item.CurrentVersion + 1
	version, err := s.buildVersion(item, nextVersion, in.Value, in.ChangeSummary, in.Actor.SubjectID, now)
	if err != nil {
		return domain.Item{}, err
	}
	if err := s.items.AppendVersion(ctx, version); err != nil {
		return domain.Item{}, translate(err)
	}

	expected := item.Version
	if in.ExpectedVersion != nil {
		expected = *in.ExpectedVersion
	}
	updated := item
	updated.CurrentVersion = nextVersion
	updated.Version = item.Version + 1
	updated.UpdatedBy = in.Actor.SubjectID
	updated.UpdatedAt = now
	if err := s.items.UpdateCAS(ctx, updated, expected); err != nil {
		return domain.Item{}, translate(err)
	}

	attrs := map[string]string{
		"env":             updated.Env,
		"namespace":       updated.Namespace,
		"key":             updated.Key,
		"current_version": fmt.Sprintf("%d", updated.CurrentVersion),
		"value_hash":      version.ValueHash,
	}
	if err := s.audit.emit(ctx, in.Actor, audit.ActionConfigChange, ResourceTypeItem,
		itemResourceID(updated), audit.OutcomeSuccess, "update_value", attrs); err != nil {
		return domain.Item{}, apperr.Internal("emit config audit failed", err)
	}
	return updated, nil
}

// TransitionInput 是治理状态迁移入参。
type TransitionInput struct {
	Env             string
	Namespace       string
	Key             string
	To              domain.ItemStatus
	ExpectedVersion *int64
	Actor           Actor
}

// Transition 迁移配置键治理状态（draft/active/deprecated/archived）。
func (s *ItemService) Transition(ctx context.Context, in TransitionInput) (domain.Item, error) {
	if err := requireActor(in.Actor); err != nil {
		return domain.Item{}, err
	}
	if !in.To.Valid() {
		return domain.Item{}, apperr.BadRequest(fmt.Sprintf("invalid item status %q", in.To))
	}
	item, err := s.get(ctx, in.Env, in.Namespace, in.Key)
	if err != nil {
		return domain.Item{}, err
	}
	if !item.Status.CanTransitionTo(in.To) {
		return domain.Item{}, apperr.Conflict(
			fmt.Sprintf("cannot transition item from %s to %s", item.Status, in.To))
	}

	expected := item.Version
	if in.ExpectedVersion != nil {
		expected = *in.ExpectedVersion
	}
	updated := item
	updated.Status = in.To
	updated.Version = item.Version + 1
	updated.UpdatedBy = in.Actor.SubjectID
	updated.UpdatedAt = s.now().UTC()
	if err := s.items.UpdateCAS(ctx, updated, expected); err != nil {
		return domain.Item{}, translate(err)
	}

	attrs := map[string]string{
		"env":       updated.Env,
		"namespace": updated.Namespace,
		"key":       updated.Key,
		"from":      string(item.Status),
		"to":        string(updated.Status),
	}
	if err := s.audit.emit(ctx, in.Actor, audit.ActionConfigChange, ResourceTypeItem,
		itemResourceID(updated), audit.OutcomeSuccess, "transition", attrs); err != nil {
		return domain.Item{}, apperr.Internal("emit config audit failed", err)
	}
	return updated, nil
}

// Get 读取配置键。
func (s *ItemService) Get(ctx context.Context, env, namespace, key string) (domain.Item, error) {
	return s.get(ctx, env, namespace, key)
}

// List 按状态过滤列出配置键。
func (s *ItemService) List(ctx context.Context, env, namespace string,
	status domain.ItemStatus, limit, offset int) ([]domain.Item, int, error) {
	if !domain.ValidEnv(env) || !domain.ValidNamespace(namespace) {
		return nil, 0, apperr.BadRequest("invalid env or namespace")
	}
	if status != "" && !status.Valid() {
		return nil, 0, apperr.BadRequest(fmt.Sprintf("invalid item status %q", status))
	}
	items, total, err := s.items.List(ctx, env, namespace, status, limit, offset)
	if err != nil {
		return nil, 0, translate(err)
	}
	return items, total, nil
}

// ListVersions 列出版本（版本升序）。
func (s *ItemService) ListVersions(ctx context.Context, itemID int64, limit, offset int) ([]domain.ItemVersion, int, error) {
	versions, total, err := s.items.ListVersions(ctx, itemID, limit, offset)
	if err != nil {
		return nil, 0, translate(err)
	}
	return versions, total, nil
}

// get 读取并校验作用域。
func (s *ItemService) get(ctx context.Context, env, namespace, key string) (domain.Item, error) {
	if !domain.ValidEnv(env) || !domain.ValidNamespace(namespace) || !domain.ValidKey(key) {
		return domain.Item{}, apperr.BadRequest("invalid env, namespace or key")
	}
	item, err := s.items.GetByKey(ctx, env, namespace, key)
	if err != nil {
		return domain.Item{}, translate(err)
	}
	return item, nil
}

// buildVersion 构造并校验一条不可变版本。
func (s *ItemService) buildVersion(item domain.Item, versionNo int64, value, summary, actorID string,
	now time.Time) (domain.ItemVersion, error) {
	versionID, err := s.ids.Next()
	if err != nil {
		return domain.ItemVersion{}, apperr.Internal("generate version id failed", err)
	}
	version := domain.ItemVersion{
		ID:            versionID,
		ItemID:        item.ID,
		Version:       versionNo,
		ValueText:     value,
		ValueHash:     domain.HashValue(value),
		ChangeSummary: summary,
		CreatedBy:     actorID,
		CreatedAt:     now,
	}
	if err := version.Validate(); err != nil {
		return domain.ItemVersion{}, apperr.BadRequest(err.Error())
	}
	return version, nil
}

// itemResourceID 返回审计资源标识：env/namespace/key。
func itemResourceID(item domain.Item) string {
	return item.Env + "/" + item.Namespace + "/" + item.Key
}

// defaultString 返回显式值，空值时回落到默认值。
func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
