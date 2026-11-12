package rest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/configcenter/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// seqIDs 是测试用确定性 ID 生成器。
type seqIDs struct {
	mu   sync.Mutex
	next int64
}

func (s *seqIDs) Next() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	return s.next, nil
}

func (s *seqIDs) NextString() (string, error) {
	next, err := s.Next()
	return strconv.FormatInt(next, 10), err
}

// quietEmitter 丢弃审计事件；审计语义由 app 层用例覆盖。
type quietEmitter struct{}

func (quietEmitter) Emit(context.Context, audit.Event) error { return nil }

// fixture 持有 Handler 与底层仓储，便于直接播种敏感项。
type fixture struct {
	handler *rest.Handler
	repos   *memory.Repositories
	ids     *seqIDs
}

// newFixture 构造内存仓储 + 两个键声明的 Handler。
func newFixture(t *testing.T) *fixture {
	t.Helper()
	repos := memory.New()
	registry := spec.NewRegistry()
	registry.MustRegister(
		domain.KeySpec{
			Namespace: "platform", Key: "http.timeout", ValueType: domain.ValueTypeDuration,
			Default: "10s", Description: "HTTP 超时", Owner: "team-platform", Reloadable: true,
		},
		domain.KeySpec{
			Namespace: "platform", Key: "log.level", ValueType: domain.ValueTypeString,
			Default: "info", Description: "日志级别", Owner: "team-platform",
		},
	)
	ids := &seqIDs{next: 100}
	itemService, err := app.NewItemService(repos.Items, registry, quietEmitter{}, ids,
		app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewItemService() error = %v", err)
	}
	releaseService, err := app.NewReleaseService(repos.Items, repos.Releases, repos.Pointers,
		quietEmitter{}, ids, app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewReleaseService() error = %v", err)
	}
	handler, err := rest.NewHandler(rest.HandlerConfig{
		Items:      itemService,
		Releases:   releaseService,
		Specs:      registry,
		PageLimits: pagination.Config{DefaultPageSize: 20, MaxPageSize: 100},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return &fixture{handler: handler, repos: repos, ids: ids}
}

// adminRequestAs 构造带 A 端 Principal 的请求。
func adminRequestAs(subject, method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	principal := auth.Principal{Channel: "admin", SubjectID: subject}
	return request.WithContext(auth.WithPrincipal(request.Context(), principal))
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func decodeEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &env); err != nil {
		t.Fatalf("response is not valid JSON: %v (body=%s)", err, recorder.Body.String())
	}
	return env
}

// createItem 走 HTTP 创建配置键并返回 item 响应。
func (f *fixture) createItem(t *testing.T, subject, body string) map[string]any {
	t.Helper()
	request := adminRequestAs(subject, http.MethodPost, "/admin/v1/configs/items", body)
	recorder := httptest.NewRecorder()
	f.handler.CreateItem(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create item status = %d (body=%s)", recorder.Code, recorder.Body.String())
	}
	env := decodeEnvelope(t, recorder)
	var data map[string]any
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	return data
}

// seedSensitiveItem 直接写仓储播种敏感项及其版本 1（管理面禁止写敏感明文）。
func (f *fixture) seedSensitiveItem(t *testing.T) (itemID, versionID int64) {
	t.Helper()
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	itemID, err := f.ids.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	versionID, err = f.ids.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	item := domain.Item{
		ID: itemID, Env: domain.EnvProd, Namespace: "platform", Key: "smtp.password",
		ValueType: domain.ValueTypeString, Description: "SMTP 密码", Owner: "team-platform",
		Status: domain.ItemStatusActive, CurrentVersion: 1, Version: 1, Sensitive: true,
		CreatedBy: "system", UpdatedBy: "system", CreatedAt: now, UpdatedAt: now,
	}
	if err := f.repos.Items.Create(context.Background(), item); err != nil {
		t.Fatalf("seed item: %v", err)
	}
	version := domain.ItemVersion{
		ID: versionID, ItemID: itemID, Version: 1,
		ValueText: "p@ssw0rd", ValueHash: domain.HashValue("p@ssw0rd"),
		ChangeSummary: "fixture", CreatedBy: "system", CreatedAt: now,
	}
	if err := f.repos.Items.AppendVersion(context.Background(), version); err != nil {
		t.Fatalf("seed version: %v", err)
	}
	return itemID, versionID
}

func TestCreateItemAndAppendValue(t *testing.T) {
	f := newFixture(t)

	item := f.createItem(t, "u1", `{"env":"prod","namespace":"platform","key":"http.timeout","value":"10s","owner":"team-platform"}`)
	if item["status"] != "draft" || item["current_version"] != float64(1) {
		t.Fatalf("unexpected item = %v", item)
	}
	if item["value"] != "10s" || item["value_hash"] == "" {
		t.Fatalf("detail must expose value for non-sensitive item: %v", item)
	}
	itemID := int64(item["id"].(float64))

	request := adminRequestAs("u1", http.MethodPut, "/admin/v1/configs/items/"+strconv.FormatInt(itemID, 10),
		`{"value":"20s","change_summary":"raise timeout","expected_version":1}`)
	request.SetPathValue("id", strconv.FormatInt(itemID, 10))
	recorder := httptest.NewRecorder()
	f.handler.UpdateValue(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("update value status = %d (body=%s)", recorder.Code, recorder.Body.String())
	}
	env := decodeEnvelope(t, recorder)
	var updated map[string]any
	if err := json.Unmarshal(env.Data, &updated); err != nil {
		t.Fatalf("decode updated item: %v", err)
	}
	if updated["current_version"] != float64(2) || updated["value"] != "20s" {
		t.Fatalf("unexpected updated item = %v", updated)
	}

	// 版本列表与单版本均可读。
	versions := adminRequestAs("u1", http.MethodGet, "/admin/v1/configs/items/1/versions", "")
	versions.SetPathValue("id", strconv.FormatInt(itemID, 10))
	versionsRecorder := httptest.NewRecorder()
	f.handler.ListVersions(versionsRecorder, versions)
	if versionsRecorder.Code != http.StatusOK {
		t.Fatalf("list versions status = %d", versionsRecorder.Code)
	}
}

func TestCreateItemRejectsUndeclaredKey(t *testing.T) {
	f := newFixture(t)
	request := adminRequestAs("u1", http.MethodPost, "/admin/v1/configs/items",
		`{"env":"prod","namespace":"billing","key":"billing.cycle","value":"monthly"}`)
	recorder := httptest.NewRecorder()
	f.handler.CreateItem(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("undeclared key status = %d (body=%s)", recorder.Code, recorder.Body.String())
	}
	if code := decodeEnvelope(t, recorder).Code; code != 40000 {
		t.Fatalf("undeclared key code = %d, want 40000", code)
	}
}

func TestPaginationRejectsOversizedPage(t *testing.T) {
	f := newFixture(t)
	request := adminRequestAs("u1", http.MethodGet,
		"/admin/v1/configs/items?env=prod&namespace=platform&page=1&page_size=101", "")
	recorder := httptest.NewRecorder()
	f.handler.ListItems(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("page_size=101 status = %d (body=%s)", recorder.Code, recorder.Body.String())
	}
}

func TestSensitiveItemNeverReturnsValue(t *testing.T) {
	f := newFixture(t)
	itemID, _ := f.seedSensitiveItem(t)
	itemPath := strconv.FormatInt(itemID, 10)

	detail := adminRequestAs("u1", http.MethodGet, "/admin/v1/configs/items/"+itemPath, "")
	detail.SetPathValue("id", itemPath)
	detailRecorder := httptest.NewRecorder()
	f.handler.GetItem(detailRecorder, detail)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("sensitive detail status = %d", detailRecorder.Code)
	}
	var item map[string]any
	if err := json.Unmarshal(decodeEnvelope(t, detailRecorder).Data, &item); err != nil {
		t.Fatalf("decode sensitive item: %v", err)
	}
	if _, exists := item["value"]; exists {
		t.Fatalf("sensitive item must not expose value: %v", item)
	}
	if item["value_hash"] == "" {
		t.Fatalf("sensitive item must expose value_hash: %v", item)
	}

	list := adminRequestAs("u1", http.MethodGet, "/admin/v1/configs/items/"+itemPath+"/versions", "")
	list.SetPathValue("id", itemPath)
	listRecorder := httptest.NewRecorder()
	f.handler.ListVersions(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("sensitive versions status = %d", listRecorder.Code)
	}
	var listData struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, listRecorder).Data, &listData); err != nil {
		t.Fatalf("decode sensitive versions: %v", err)
	}
	if len(listData.Items) != 1 {
		t.Fatalf("sensitive versions count = %d", len(listData.Items))
	}
	if _, exists := listData.Items[0]["value"]; exists {
		t.Fatalf("sensitive version must not expose value: %v", listData.Items[0])
	}

	single := adminRequestAs("u1", http.MethodGet, "/admin/v1/configs/items/"+itemPath+"/versions/1", "")
	single.SetPathValue("id", itemPath)
	single.SetPathValue("version", "1")
	singleRecorder := httptest.NewRecorder()
	f.handler.GetVersion(singleRecorder, single)
	if singleRecorder.Code != http.StatusOK {
		t.Fatalf("sensitive version status = %d", singleRecorder.Code)
	}
	var version map[string]any
	if err := json.Unmarshal(decodeEnvelope(t, singleRecorder).Data, &version); err != nil {
		t.Fatalf("decode sensitive version: %v", err)
	}
	if _, exists := version["value"]; exists {
		t.Fatalf("sensitive version must not expose value: %v", version)
	}
}

func TestReleaseLifecycle(t *testing.T) {
	f := newFixture(t)
	item := f.createItem(t, "u1", `{"env":"prod","namespace":"platform","key":"log.level","value":"info","owner":"team-platform"}`)
	itemID := int64(item["id"].(float64))
	itemPath := strconv.FormatInt(itemID, 10)

	// 创建 draft 发布单。
	createRelease := func(version int64, name string) int64 {
		body, err := json.Marshal(map[string]any{
			"env": "prod", "namespace": "platform", "name": name, "strategy": "all",
			"entries": []map[string]any{{"item_id": itemID, "item_version": version}},
		})
		if err != nil {
			t.Fatalf("marshal release body: %v", err)
		}
		request := adminRequestAs("u1", http.MethodPost, "/admin/v1/configs/releases", string(body))
		recorder := httptest.NewRecorder()
		f.handler.CreateRelease(recorder, request)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("create release status = %d (body=%s)", recorder.Code, recorder.Body.String())
		}
		var data map[string]any
		if err := json.Unmarshal(decodeEnvelope(t, recorder).Data, &data); err != nil {
			t.Fatalf("decode release: %v", err)
		}
		return int64(data["id"].(float64))
	}
	submit := func(releaseID int64) {
		request := adminRequestAs("u1", http.MethodPost, "/admin/v1/configs/releases/1/submit", "")
		request.SetPathValue("id", strconv.FormatInt(releaseID, 10))
		recorder := httptest.NewRecorder()
		f.handler.SubmitRelease(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("submit release status = %d (body=%s)", recorder.Code, recorder.Body.String())
		}
	}
	approve := func(releaseID int64, subject string) *httptest.ResponseRecorder {
		request := adminRequestAs(subject, http.MethodPost, "/admin/v1/configs/releases/1/approve",
			`{"decision":"approved","comment":"ok"}`)
		request.SetPathValue("id", strconv.FormatInt(releaseID, 10))
		recorder := httptest.NewRecorder()
		f.handler.ApproveRelease(recorder, request)
		return recorder
	}
	publish := func(releaseID int64) {
		request := adminRequestAs("u3", http.MethodPost, "/admin/v1/configs/releases/1/publish", `{}`)
		request.SetPathValue("id", strconv.FormatInt(releaseID, 10))
		recorder := httptest.NewRecorder()
		f.handler.PublishRelease(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("publish release status = %d (body=%s)", recorder.Code, recorder.Body.String())
		}
	}

	releaseA := createRelease(1, "first release")
	submit(releaseA)
	// 提交人自审必须 403。
	selfReview := approve(releaseA, "u1")
	if selfReview.Code != http.StatusForbidden {
		t.Fatalf("self review status = %d (body=%s)", selfReview.Code, selfReview.Body.String())
	}
	if code := decodeEnvelope(t, selfReview).Code; code != 40300 {
		t.Fatalf("self review code = %d, want 40300", code)
	}
	if recorder := approve(releaseA, "u2"); recorder.Code != http.StatusOK {
		t.Fatalf("approve status = %d (body=%s)", recorder.Code, recorder.Body.String())
	}
	publish(releaseA)

	// 追加值后创建第二个发布单并生效。
	updateRequest := adminRequestAs("u1", http.MethodPut, "/admin/v1/configs/items/"+itemPath,
		`{"value":"debug","change_summary":"debug","expected_version":1}`)
	updateRequest.SetPathValue("id", itemPath)
	updateRecorder := httptest.NewRecorder()
	f.handler.UpdateValue(updateRecorder, updateRequest)
	if updateRecorder.Code != http.StatusOK {
		t.Fatalf("update value status = %d (body=%s)", updateRecorder.Code, updateRecorder.Body.String())
	}
	releaseB := createRelease(2, "second release")
	submit(releaseB)
	if recorder := approve(releaseB, "u2"); recorder.Code != http.StatusOK {
		t.Fatalf("approve B status = %d (body=%s)", recorder.Code, recorder.Body.String())
	}
	publish(releaseB)

	// 回滚到 A：服务端按目标快照生成新 Release 并生效。
	rollbackRequest := adminRequestAs("u3", http.MethodPost, "/admin/v1/configs/releases/1/rollback",
		`{"comment":"rollback to first"}`)
	rollbackRequest.SetPathValue("id", strconv.FormatInt(releaseA, 10))
	rollbackRecorder := httptest.NewRecorder()
	f.handler.RollbackRelease(rollbackRecorder, rollbackRequest)
	if rollbackRecorder.Code != http.StatusOK {
		t.Fatalf("rollback status = %d (body=%s)", rollbackRecorder.Code, rollbackRecorder.Body.String())
	}
	var rollbackRelease map[string]any
	if err := json.Unmarshal(decodeEnvelope(t, rollbackRecorder).Data, &rollbackRelease); err != nil {
		t.Fatalf("decode rollback release: %v", err)
	}
	if rollbackRelease["status"] != "active" {
		t.Fatalf("rollback release status = %v", rollbackRelease["status"])
	}

	// 详情同时返回 items 与 reviews。
	detail := adminRequestAs("u1", http.MethodGet, "/admin/v1/configs/releases/1", "")
	detail.SetPathValue("id", strconv.FormatInt(releaseA, 10))
	detailRecorder := httptest.NewRecorder()
	f.handler.GetRelease(detailRecorder, detail)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("release detail status = %d", detailRecorder.Code)
	}
	var detailData map[string]any
	if err := json.Unmarshal(decodeEnvelope(t, detailRecorder).Data, &detailData); err != nil {
		t.Fatalf("decode release detail: %v", err)
	}
	if len(detailData["items"].([]any)) != 1 || len(detailData["reviews"].([]any)) != 1 {
		t.Fatalf("release detail must include items and reviews: %v", detailData)
	}
}
