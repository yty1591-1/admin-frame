// Package rest 是配置中心的 A 端 HTTP Handler。
//
// 只做参数解析、用例调用与响应封装：状态机、快照与审计在 domain / app，
// 本包不得出现「哪些状态能迁移」之类的判断。写接口在 transport 层用
// pkg/db.WithinUnitOfWork 包事务（对齐 tenant / featureflag 的装配约定），
// 内存模式（SQLDB 为 nil）直接执行。
//
// 敏感项（Sensitive=true）的值不进入管理面响应：详情、版本列表与单版本
// 只返回 value_hash，不返回 value。
package rest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// maxBodyBytes 管理类请求体上限：配置值可能是 JSON，64KB 足够。
const maxBodyBytes = 64 << 10

// namespaceFromPath 读取路径参数 namespace；兼容未经 ServeMux 匹配的直调测试。
func namespaceFromPath(r *http.Request) string {
	if ns := strings.TrimSpace(r.PathValue("namespace")); ns != "" {
		return ns
	}
	const marker = "/namespaces/"
	path := r.URL.Path
	idx := strings.Index(path, marker)
	if idx < 0 {
		return ""
	}
	rest := path[idx+len(marker):]
	if end := strings.IndexByte(rest, '/'); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

// Handler 是配置中心管理 Handler。
type Handler struct {
	items     *app.ItemService
	releases  *app.ReleaseService
	secrets   *app.SecretService
	specs     *spec.Registry
	pageCfg   pagination.Config
	sqlDB     *sql.DB
	deployEnv string // 仅 internal resolve 授权使用；管理面可空
}

// HandlerConfig 是 Handler 的装配参数。
type HandlerConfig struct {
	// Items 配置键用例；必填。
	Items *app.ItemService
	// Releases 发布用例；必填。
	Releases *app.ReleaseService
	// Secrets 业务密钥用例；M2 可选，nil 时密钥端点返回 500。
	Secrets *app.SecretService
	// Specs 代码键声明注册表；用于 namespace 列表与键数量。
	Specs *spec.Registry
	// PageLimits 分页默认值与上限（page_size ≤ 100）。
	PageLimits pagination.Config
	// SQLDB 用于写接口事务；内存模式为 nil（直接执行）。
	SQLDB *sql.DB
}

// NewHandler 构造 Handler。
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	switch {
	case cfg.Items == nil:
		return nil, apperr.Internal("configcenter handler requires an item service", nil)
	case cfg.Releases == nil:
		return nil, apperr.Internal("configcenter handler requires a release service", nil)
	case cfg.Specs == nil:
		return nil, apperr.Internal("configcenter handler requires a key spec registry", nil)
	}
	return &Handler{
		items:    cfg.Items,
		releases: cfg.Releases,
		secrets:  cfg.Secrets,
		specs:    cfg.Specs,
		pageCfg:  cfg.PageLimits,
		sqlDB:    cfg.SQLDB,
	}, nil
}

// withinTx 在 SQL 模式下包事务；内存模式直接执行。
func (h *Handler) withinTx(ctx context.Context, fn func(context.Context) error) error {
	if h.sqlDB == nil {
		return fn(ctx)
	}
	return db.WithinUnitOfWork(ctx, h.sqlDB, nil, fn)
}

// actor 从鉴权主体构造审计主体；管理面端别固定为 admin。
func actor(r *http.Request) (app.Actor, error) {
	principal, ok := auth.FromContext(r.Context())
	if !ok || strings.TrimSpace(principal.SubjectID) == "" {
		return app.Actor{}, apperr.Unauthorized("authentication required")
	}
	return app.Actor{
		SubjectID:  principal.SubjectID,
		ActorType:  "user",
		Channel:    "admin",
		MerchantID: principal.MerchantID,
		TraceID:    apiresp.TraceIDFromContext(r.Context()),
	}, nil
}

// ---------------------------------------------------------------- namespace

type namespaceView struct {
	Namespace string `json:"namespace"`
	KeyCount  int    `json:"key_count"`
}

type namespaceListResponse struct {
	Items []namespaceView `json:"items"`
	Total int             `json:"total"`
}

// ListNamespaces 处理 GET /admin/v1/configs/namespaces。
func (h *Handler) ListNamespaces(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	namespaces := h.specs.Namespaces()
	items := make([]namespaceView, 0, len(namespaces))
	for _, namespace := range namespaces {
		items = append(items, namespaceView{
			Namespace: namespace,
			KeyCount:  len(h.specs.List(namespace)),
		})
	}
	apiresp.OK(w, namespaceListResponse{Items: items, Total: len(items)}, traceID)
}

type declaredKeyView struct {
	Key          string   `json:"key"`
	ValueType    string   `json:"value_type"`
	Default      string   `json:"default,omitempty"`
	Description  string   `json:"description"`
	Owner        string   `json:"owner"`
	ReloadMode   string   `json:"reload_mode"`
	HotWhitelist bool     `json:"hot_whitelist"`
	Sensitive    bool     `json:"sensitive"`
	Required     bool     `json:"required"`
	Deprecated   bool     `json:"deprecated"`
	Enum         []string `json:"enum,omitempty"`
}

type declaredKeyListResponse struct {
	Items []declaredKeyView `json:"items"`
	Total int               `json:"total"`
}

// ListDeclaredKeys 处理 GET /admin/v1/configs/namespaces/{namespace}/keys（CC-R-12/17）。
func (h *Handler) ListDeclaredKeys(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	namespace := namespaceFromPath(r)
	if !domain.ValidNamespace(namespace) {
		apiresp.FromError(w, apperr.BadRequest("invalid namespace"), traceID)
		return
	}
	hotSet := spec.HotKeySet()
	specs := h.specs.List(namespace)
	items := make([]declaredKeyView, 0, len(specs))
	for _, keySpec := range specs {
		_, hot := hotSet[keySpec.Key]
		items = append(items, declaredKeyView{
			Key:          keySpec.Key,
			ValueType:    string(keySpec.ValueType),
			Default:      keySpec.Default,
			Description:  keySpec.Description,
			Owner:        keySpec.Owner,
			ReloadMode:   string(keySpec.EffectiveReloadMode()),
			HotWhitelist: hot,
			Sensitive:    keySpec.Sensitive,
			Required:     keySpec.Required,
			Deprecated:   keySpec.Deprecated,
			Enum:         keySpec.Enum,
		})
	}
	apiresp.OK(w, declaredKeyListResponse{Items: items, Total: len(items)}, traceID)
}

// ---------------------------------------------------------------- item

// itemResponse 是配置键元数据视图；value / value_hash 仅在详情与
// 非敏感场景出现，敏感项不得输出 value。
type itemResponse struct {
	ID             int64   `json:"id"`
	Env            string  `json:"env"`
	Namespace      string  `json:"namespace"`
	Key            string  `json:"key"`
	ValueType      string  `json:"value_type"`
	Description    string  `json:"description"`
	Owner          string  `json:"owner"`
	Status         string  `json:"status"`
	CurrentVersion int64   `json:"current_version"`
	Version        int64   `json:"version"`
	Sensitive      bool    `json:"sensitive"`
	ExpiresAt      *string `json:"expires_at"`
	CreatedBy      string  `json:"created_by"`
	UpdatedBy      string  `json:"updated_by"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	ValueHash      string  `json:"value_hash,omitempty"`
	Value          *string `json:"value,omitempty"`
}

type itemListResponse struct {
	Items    []itemResponse `json:"items"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
}

// toItemView 聚合 → 元数据视图（不含值）。
func toItemView(item domain.Item) itemResponse {
	return itemResponse{
		ID:             item.ID,
		Env:            item.Env,
		Namespace:      item.Namespace,
		Key:            item.Key,
		ValueType:      string(item.ValueType),
		Description:    item.Description,
		Owner:          item.Owner,
		Status:         string(item.Status),
		CurrentVersion: item.CurrentVersion,
		Version:        item.Version,
		Sensitive:      item.Sensitive,
		ExpiresAt:      formatTimePtr(item.ExpiresAt),
		CreatedBy:      item.CreatedBy,
		UpdatedBy:      item.UpdatedBy,
		CreatedAt:      formatTime(item.CreatedAt),
		UpdatedAt:      formatTime(item.UpdatedAt),
	}
}

// itemDetail 在元数据视图上补充当前版本的 hash；非敏感项同时返回值。
func (h *Handler) itemDetail(ctx context.Context, item domain.Item) (itemResponse, error) {
	view := toItemView(item)
	if item.CurrentVersion <= 0 {
		return view, nil
	}
	version, err := h.items.GetVersion(ctx, item.ID, item.CurrentVersion)
	if err != nil {
		return itemResponse{}, err
	}
	view.ValueHash = version.ValueHash
	if !item.Sensitive {
		value := version.ValueText
		view.Value = &value
	}
	return view, nil
}

// ListItems 处理 GET /admin/v1/configs/items。
func (h *Handler) ListItems(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	page, err := pagination.ParseWith(r, h.pageCfg)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	query := r.URL.Query()
	items, total, err := h.items.List(r.Context(),
		strings.TrimSpace(query.Get("env")),
		strings.TrimSpace(query.Get("namespace")),
		domain.ItemStatus(strings.TrimSpace(query.Get("status"))),
		page.Limit(), page.Offset())
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	views := make([]itemResponse, 0, len(items))
	for _, item := range items {
		views = append(views, toItemView(item))
	}
	apiresp.OK(w, itemListResponse{
		Items: views, Total: total, Page: page.Page, PageSize: page.PageSize,
	}, traceID)
}

type createItemRequest struct {
	Env           string     `json:"env"`
	Namespace     string     `json:"namespace"`
	Key           string     `json:"key"`
	Value         *string    `json:"value"`
	Description   string     `json:"description"`
	Owner         string     `json:"owner"`
	Status        string     `json:"status"`
	ExpiresAt     *time.Time `json:"expires_at"`
	ChangeSummary string     `json:"change_summary"`
}

// CreateItem 处理 POST /admin/v1/configs/items。
func (h *Handler) CreateItem(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	auditActor, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body createItemRequest
	if err := decodeJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	var item domain.Item
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		created, createErr := h.items.CreateItem(ctx, app.CreateItemInput{
			Env:           body.Env,
			Namespace:     body.Namespace,
			Key:           body.Key,
			Value:         body.Value,
			Description:   body.Description,
			Owner:         body.Owner,
			Status:        domain.ItemStatus(body.Status),
			ExpiresAt:     body.ExpiresAt,
			ChangeSummary: body.ChangeSummary,
			Actor:         auditActor,
		})
		if createErr != nil {
			return createErr
		}
		item = created
		return nil
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	view, err := h.itemDetail(r.Context(), item)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.JSON(w, http.StatusCreated, 0, "created", view, traceID)
}

// GetItem 处理 GET /admin/v1/configs/items/{id}。
func (h *Handler) GetItem(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	item, err := h.items.GetByID(r.Context(), id)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	view, err := h.itemDetail(r.Context(), item)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, view, traceID)
}

type updateValueRequest struct {
	Value           *string `json:"value"`
	ChangeSummary   string  `json:"change_summary"`
	ExpectedVersion *int64  `json:"expected_version"`
}

type transitionItemRequest struct {
	To              string `json:"to"`
	ExpectedVersion *int64 `json:"expected_version"`
}

// UpdateValue 处理 PUT /admin/v1/configs/items/{id}（只追加新版本）。
func (h *Handler) UpdateValue(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	auditActor, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body updateValueRequest
	if err := decodeJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	if body.Value == nil {
		apiresp.FromError(w, apperr.BadRequest("value is required"), traceID)
		return
	}
	var item domain.Item
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		current, getErr := h.items.GetByID(ctx, id)
		if getErr != nil {
			return getErr
		}
		updated, updateErr := h.items.UpdateValue(ctx, app.UpdateValueInput{
			Env:             current.Env,
			Namespace:       current.Namespace,
			Key:             current.Key,
			Value:           *body.Value,
			ChangeSummary:   body.ChangeSummary,
			ExpectedVersion: body.ExpectedVersion,
			Actor:           auditActor,
		})
		if updateErr != nil {
			return updateErr
		}
		item = updated
		return nil
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	view, err := h.itemDetail(r.Context(), item)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, view, traceID)
}

// TransitionItem 处理 POST /admin/v1/configs/items/{id}/transition（CC-R-06）。
func (h *Handler) TransitionItem(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	auditActor, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body transitionItemRequest
	if err := decodeJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	to := domain.ItemStatus(strings.TrimSpace(body.To))
	var item domain.Item
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		current, getErr := h.items.GetByID(ctx, id)
		if getErr != nil {
			return getErr
		}
		updated, transitionErr := h.items.Transition(ctx, app.TransitionInput{
			Env:             current.Env,
			Namespace:       current.Namespace,
			Key:             current.Key,
			To:              to,
			ExpectedVersion: body.ExpectedVersion,
			Actor:           auditActor,
		})
		if transitionErr != nil {
			return transitionErr
		}
		item = updated
		return nil
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	view, err := h.itemDetail(r.Context(), item)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, view, traceID)
}

// ---------------------------------------------------------------- version

type itemVersionResponse struct {
	ID            int64   `json:"id"`
	ItemID        int64   `json:"item_id"`
	Version       int64   `json:"version"`
	ValueHash     string  `json:"value_hash"`
	ChangeSummary string  `json:"change_summary"`
	CreatedBy     string  `json:"created_by"`
	CreatedAt     string  `json:"created_at"`
	Value         *string `json:"value,omitempty"`
}

type versionListResponse struct {
	Items    []itemVersionResponse `json:"items"`
	Total    int                   `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
}

// toVersionView 版本 → 视图；敏感项不输出 value。
func toVersionView(version domain.ItemVersion, sensitive bool) itemVersionResponse {
	view := itemVersionResponse{
		ID:            version.ID,
		ItemID:        version.ItemID,
		Version:       version.Version,
		ValueHash:     version.ValueHash,
		ChangeSummary: version.ChangeSummary,
		CreatedBy:     version.CreatedBy,
		CreatedAt:     formatTime(version.CreatedAt),
	}
	if !sensitive {
		value := version.ValueText
		view.Value = &value
	}
	return view
}

// ListVersions 处理 GET /admin/v1/configs/items/{id}/versions。
func (h *Handler) ListVersions(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	page, err := pagination.ParseWith(r, h.pageCfg)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	item, err := h.items.GetByID(r.Context(), id)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	versions, total, err := h.items.ListVersions(r.Context(), id, page.Limit(), page.Offset())
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	views := make([]itemVersionResponse, 0, len(versions))
	for _, version := range versions {
		views = append(views, toVersionView(version, item.Sensitive))
	}
	apiresp.OK(w, versionListResponse{
		Items: views, Total: total, Page: page.Page, PageSize: page.PageSize,
	}, traceID)
}

// GetVersion 处理 GET /admin/v1/configs/items/{id}/versions/{version}。
func (h *Handler) GetVersion(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	versionNo, err := parsePositive(r.PathValue("version"), "version")
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	item, err := h.items.GetByID(r.Context(), id)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	version, err := h.items.GetVersion(r.Context(), id, versionNo)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toVersionView(version, item.Sensitive), traceID)
}

// ---------------------------------------------------------------- release

type releaseResponse struct {
	ID                int64               `json:"id"`
	Env               string              `json:"env"`
	Namespace         string              `json:"namespace"`
	ReleaseNo         int64               `json:"release_no"`
	Name              string              `json:"name"`
	Description       string              `json:"description"`
	Status            string              `json:"status"`
	Strategy          string              `json:"strategy"`
	TargetRuleJSON    string              `json:"target_rule_json"`
	SnapshotHash      string              `json:"snapshot_hash"`
	SnapshotSize      int                 `json:"snapshot_size"`
	SnapshotKeyID     string              `json:"snapshot_key_id,omitempty"`
	SnapshotSignature string              `json:"snapshot_signature,omitempty"`
	BasedOnReleaseID  *int64              `json:"based_on_release_id"`
	CreatedBy         string              `json:"created_by"`
	SubmittedBy       string              `json:"submitted_by"`
	ApprovedBy        string              `json:"approved_by"`
	ApprovedAt        *string             `json:"approved_at"`
	PublishedAt       *string             `json:"published_at"`
	CreatedAt         string              `json:"created_at"`
	UpdatedAt         string              `json:"updated_at"`
	Items             []releaseItemView   `json:"items,omitempty"`
	Reviews           []releaseReviewView `json:"reviews,omitempty"`
}

type releaseItemView struct {
	ItemID      int64  `json:"item_id"`
	ItemVersion int64  `json:"item_version"`
	ConfigKey   string `json:"config_key"`
	ValueHash   string `json:"value_hash"`
}

type releaseReviewView struct {
	ID        int64  `json:"id"`
	ReleaseID int64  `json:"release_id"`
	Reviewer  string `json:"reviewer"`
	Decision  string `json:"decision"`
	Comment   string `json:"comment"`
	CreatedAt string `json:"created_at"`
}

type releaseListResponse struct {
	Items    []releaseResponse `json:"items"`
	Total    int               `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
}

func toReleaseView(release domain.Release) releaseResponse {
	return releaseResponse{
		ID:                release.ID,
		Env:               release.Env,
		Namespace:         release.Namespace,
		ReleaseNo:         release.ReleaseNo,
		Name:              release.Name,
		Description:       release.Description,
		Status:            string(release.Status),
		Strategy:          string(release.Strategy),
		TargetRuleJSON:    release.TargetRuleJSON,
		SnapshotHash:      release.SnapshotHash,
		SnapshotSize:      release.SnapshotSize,
		SnapshotKeyID:     release.SnapshotKeyID,
		SnapshotSignature: release.SnapshotSignature,
		BasedOnReleaseID:  release.BasedOnReleaseID,
		CreatedBy:         release.CreatedBy,
		SubmittedBy:       release.SubmittedBy,
		ApprovedBy:        release.ApprovedBy,
		ApprovedAt:        formatTimePtr(release.ApprovedAt),
		PublishedAt:       formatTimePtr(release.PublishedAt),
		CreatedAt:         formatTime(release.CreatedAt),
		UpdatedAt:         formatTime(release.UpdatedAt),
	}
}

type releaseEntryRequest struct {
	ItemID      int64 `json:"item_id"`
	ItemVersion int64 `json:"item_version"`
}

type createReleaseRequest struct {
	Env              string                `json:"env"`
	Namespace        string                `json:"namespace"`
	Name             string                `json:"name"`
	Description      string                `json:"description"`
	Strategy         string                `json:"strategy"`
	TargetRuleJSON   string                `json:"target_rule_json"`
	Entries          []releaseEntryRequest `json:"entries"`
	BasedOnReleaseID *int64                `json:"based_on_release_id"`
}

// CreateRelease 处理 POST /admin/v1/configs/releases。
func (h *Handler) CreateRelease(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	auditActor, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body createReleaseRequest
	if err := decodeJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	strategy := domain.ReleaseStrategy(strings.TrimSpace(body.Strategy))
	if strategy == "" {
		strategy = domain.StrategyAll
	}
	entries := make([]app.SnapshotEntry, 0, len(body.Entries))
	for _, entry := range body.Entries {
		entries = append(entries, app.SnapshotEntry{ItemID: entry.ItemID, ItemVersion: entry.ItemVersion})
	}
	var release domain.Release
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		created, createErr := h.releases.CreateRelease(ctx, app.CreateReleaseInput{
			Env:              body.Env,
			Namespace:        body.Namespace,
			Name:             body.Name,
			Description:      body.Description,
			Strategy:         strategy,
			TargetRuleJSON:   body.TargetRuleJSON,
			Entries:          entries,
			BasedOnReleaseID: body.BasedOnReleaseID,
			Actor:            auditActor,
		})
		if createErr != nil {
			return createErr
		}
		release = created
		return nil
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.JSON(w, http.StatusCreated, 0, "created", toReleaseView(release), traceID)
}

// ListReleases 处理 GET /admin/v1/configs/releases。
func (h *Handler) ListReleases(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	page, err := pagination.ParseWith(r, h.pageCfg)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	query := r.URL.Query()
	owner := strings.TrimSpace(query.Get("owner"))
	if owner == "" {
		owner = strings.TrimSpace(query.Get("created_by"))
	}
	releases, total, err := h.releases.List(r.Context(), domain.ReleaseListFilter{
		Env:       strings.TrimSpace(query.Get("env")),
		Namespace: strings.TrimSpace(query.Get("namespace")),
		Status:    domain.ReleaseStatus(strings.TrimSpace(query.Get("status"))),
		Keyword:   strings.TrimSpace(query.Get("keyword")),
		Owner:     owner,
	}, page.Limit(), page.Offset())
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	views := make([]releaseResponse, 0, len(releases))
	for _, release := range releases {
		views = append(views, toReleaseView(release))
	}
	apiresp.OK(w, releaseListResponse{
		Items: views, Total: total, Page: page.Page, PageSize: page.PageSize,
	}, traceID)
}

// GetRelease 处理 GET /admin/v1/configs/releases/{id}（release + items + reviews）。
func (h *Handler) GetRelease(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	release, err := h.releases.Get(r.Context(), id)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	items, err := h.releases.ListItems(r.Context(), id)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	reviews, err := h.releases.ListReviews(r.Context(), id)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	view := toReleaseView(release)
	view.Items = make([]releaseItemView, 0, len(items))
	for _, item := range items {
		view.Items = append(view.Items, releaseItemView{
			ItemID:      item.ItemID,
			ItemVersion: item.ItemVersion,
			ConfigKey:   item.ConfigKey,
			ValueHash:   item.ValueHash,
		})
	}
	view.Reviews = make([]releaseReviewView, 0, len(reviews))
	for _, review := range reviews {
		view.Reviews = append(view.Reviews, releaseReviewView{
			ID:        review.ID,
			ReleaseID: review.ReleaseID,
			Reviewer:  review.Reviewer,
			Decision:  string(review.Decision),
			Comment:   review.Comment,
			CreatedAt: formatTime(review.CreatedAt),
		})
	}
	apiresp.OK(w, view, traceID)
}

// SubmitRelease 处理 POST /admin/v1/configs/releases/{id}/submit。
func (h *Handler) SubmitRelease(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	auditActor, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var release domain.Release
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		updated, submitErr := h.releases.Submit(ctx, id, auditActor)
		if submitErr != nil {
			return submitErr
		}
		release = updated
		return nil
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toReleaseView(release), traceID)
}

type approveRequest struct {
	Decision string `json:"decision"`
	Comment  string `json:"comment"`
}

// ApproveRelease 处理 POST /admin/v1/configs/releases/{id}/approve。
func (h *Handler) ApproveRelease(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	auditActor, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body approveRequest
	if err := decodeJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	decision := domain.ReviewDecision(strings.TrimSpace(body.Decision))
	if !decision.Valid() {
		apiresp.FromError(w, apperr.BadRequest("decision must be approved or rejected"), traceID)
		return
	}
	var release domain.Release
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		updated, approveErr := h.releases.Approve(ctx, app.ApproveInput{
			ReleaseID: id,
			Decision:  decision,
			Comment:   body.Comment,
			Actor:     auditActor,
		})
		if approveErr != nil {
			return approveErr
		}
		release = updated
		return nil
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toReleaseView(release), traceID)
}

type cancelReleaseRequest struct {
	Reason string `json:"reason"`
}

// CancelRelease 处理 POST /admin/v1/configs/releases/{id}/cancel（CC-R-16）。
func (h *Handler) CancelRelease(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	auditActor, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body cancelReleaseRequest
	if err := decodeJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	var release domain.Release
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		updated, cancelErr := h.releases.Cancel(ctx, id, body.Reason, auditActor)
		if cancelErr != nil {
			return cancelErr
		}
		release = updated
		return nil
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toReleaseView(release), traceID)
}

type publishRequest struct {
	ExpectedPointerVersion *int64 `json:"expected_pointer_version"`
}

// PublishRelease 处理 POST /admin/v1/configs/releases/{id}/publish。
func (h *Handler) PublishRelease(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	auditActor, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body publishRequest
	if err := decodeOptionalJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	var release domain.Release
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		updated, publishErr := h.releases.Publish(ctx, id, body.ExpectedPointerVersion, auditActor)
		if publishErr != nil {
			return publishErr
		}
		release = updated
		return nil
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toReleaseView(release), traceID)
}

type rollbackRequest struct {
	ExpectedPointerVersion *int64 `json:"expected_pointer_version"`
	Comment                string `json:"comment"`
	BreakGlass             bool   `json:"break_glass"`
	BreakGlassReason       string `json:"break_glass_reason"`
}

// RollbackRelease 处理 POST /admin/v1/configs/releases/{id}/rollback。
func (h *Handler) RollbackRelease(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	auditActor, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body rollbackRequest
	if err := decodeOptionalJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	var release domain.Release
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		updated, rollbackErr := h.releases.Rollback(ctx, app.RollbackInput{
			TargetReleaseID:        id,
			Description:            body.Comment,
			ExpectedPointerVersion: body.ExpectedPointerVersion,
			BreakGlass:             body.BreakGlass,
			BreakGlassReason:       body.BreakGlassReason,
			Actor:                  auditActor,
		})
		if rollbackErr != nil {
			return rollbackErr
		}
		release = updated
		return nil
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toReleaseView(release), traceID)
}

// ---------------------------------------------------------------- helpers

// parseID 解析路径中的正整数主键。
func parseID(raw string) (int64, error) {
	return parsePositive(raw, "id")
}

// parsePositive 解析路径中的正整数。
func parsePositive(raw, field string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value <= 0 {
		return 0, apperr.BadRequest("invalid " + field)
	}
	return value, nil
}

// decodeJSON 解析必需请求体：空 body 视为错误。
func decodeJSON(r *http.Request, dst any) error {
	return decodeBody(r, dst, true)
}

// decodeOptionalJSON 解析可选请求体：空 body 视为零值。
func decodeOptionalJSON(r *http.Request, dst any) error {
	return decodeBody(r, dst, false)
}

// decodeBody 统一处理大小限制、未知字段与尾随垃圾。
func decodeBody(r *http.Request, dst any, required bool) error {
	if r.Body == nil {
		if required {
			return errors.New("request body is required")
		}
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(dst)
	if errors.Is(err, io.EOF) {
		if required {
			return errors.New("request body is required")
		}
		return nil
	}
	if err != nil {
		return errors.New("invalid JSON body")
	}
	if decoder.More() {
		return errors.New("unexpected trailing data in body")
	}
	return nil
}

// formatTime 输出 UTC RFC3339；零值输出空串而不是 0001-01-01。
func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

// formatTimePtr 输出可空时间。
func formatTimePtr(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := formatTime(*value)
	return &formatted
}
