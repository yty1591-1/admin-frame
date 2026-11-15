package admin_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/configcenter/transport/admin"
	"github.com/admin-frame/admin-frame/internal/configcenter/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/configclient"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// testNow 固定发布与审计时间，便于断言快照元数据。
var testNow = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

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

// apiResponse 是管理面统一响应信封。
type apiResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type itemDTO struct {
	ID             int64  `json:"id"`
	CurrentVersion int64  `json:"current_version"`
	Value          string `json:"value"`
}

type releaseDTO struct {
	ID                int64  `json:"id"`
	ReleaseNo         int64  `json:"release_no"`
	Status            string `json:"status"`
	SnapshotHash      string `json:"snapshot_hash"`
	SnapshotKeyID     string `json:"snapshot_key_id"`
	SnapshotSignature string `json:"snapshot_signature"`
}

// testAuth 从请求头注入 A 端主体与权限码。
func testAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject := strings.TrimSpace(r.Header.Get("X-Test-Subject"))
		if subject == "" {
			http.Error(w, `{"code":40100,"message":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}
		var scopes []string
		for _, scope := range strings.Split(r.Header.Get("X-Test-Scopes"), ",") {
			if trimmed := strings.TrimSpace(scope); trimmed != "" {
				scopes = append(scopes, trimmed)
			}
		}
		principal := auth.Principal{Channel: auth.ChannelAdmin, SubjectID: subject, Scopes: scopes}
		next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
	})
}

// testPermissions 校验主体是否持有指定权限码。
func testPermissions(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, _ := auth.FromContext(r.Context())
			for _, scope := range principal.Scopes {
				if scope == permission {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, `{"code":40300,"message":"forbidden"}`, http.StatusForbidden)
		})
	}
}

// do 发起管理面请求并解码统一信封；401/403 响应体可能不是 JSON，忽略解码错误。
func do(t *testing.T, method, target, subject string, scopes []string, body any) (int, apiResponse) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, target, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if subject != "" {
		request.Header.Set("X-Test-Subject", subject)
	}
	request.Header.Set("X-Test-Scopes", strings.Join(scopes, ","))
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer func() { _ = response.Body.Close() }()
	var envelope apiResponse
	_ = json.NewDecoder(response.Body).Decode(&envelope)
	return response.StatusCode, envelope
}

// newIntegrationServer 装配内存仓储 + 签名发布 + 管理路由。
func newIntegrationServer(t *testing.T) (*httptest.Server, *memory.Repositories, *configclient.Verifier) {
	t.Helper()
	repos := memory.New()
	registry := spec.NewRegistry()
	registry.MustRegister(
		domain.KeySpec{
			Namespace: "platform", Key: "log.level", ValueType: domain.ValueTypeString,
			Default: "info", Description: "日志级别", Owner: "team-platform",
			Reloadable: true, Enum: []string{"debug", "info", "warn"},
		},
		domain.KeySpec{
			Namespace: "platform", Key: "smtp.password", ValueType: domain.ValueTypeString,
			Description: "SMTP 口令", Owner: "team-platform", Sensitive: true,
		},
	)
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	signer, err := configclient.NewSigner("k1", privateKey)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	verifier, err := configclient.NewVerifier(map[string]ed25519.PublicKey{"k1": publicKey})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	ids := &seqIDs{next: 1000}
	itemService, err := app.NewItemService(repos.Items, registry, quietEmitter{}, ids,
		app.Options{Service: "admin-api", Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("new item service: %v", err)
	}
	releaseService, err := app.NewReleaseService(repos.Items, repos.Releases, repos.Pointers,
		quietEmitter{}, ids, app.Options{
			Service: "admin-api", Now: func() time.Time { return testNow }, Signer: signer,
		})
	if err != nil {
		t.Fatalf("new release service: %v", err)
	}
	handler, err := rest.NewHandler(rest.HandlerConfig{
		Items:      itemService,
		Releases:   releaseService,
		Specs:      registry,
		PageLimits: pagination.Config{DefaultPageSize: 20, MaxPageSize: 100},
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	server := httptest.NewServer(admin.NewRouter(handler, testAuth, testPermissions))
	t.Cleanup(server.Close)
	return server, repos, verifier
}

func decodeData[T any](t *testing.T, envelope apiResponse) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(envelope.Data, &value); err != nil {
		t.Fatalf("decode response data: %v (data=%s)", err, envelope.Data)
	}
	return value
}

// seedSensitiveItem 直接写仓储播种敏感项与版本 1（管理面禁止写敏感明文）。
func seedSensitiveItem(t *testing.T, repos *memory.Repositories, id int64, key, value string) int64 {
	t.Helper()
	item := domain.Item{
		ID: id, Env: domain.EnvDev, Namespace: "platform", Key: key,
		ValueType: domain.ValueTypeString, Description: "敏感项", Owner: "team-platform",
		Sensitive: true, Status: domain.ItemStatusActive, CurrentVersion: 1, Version: 1,
		CreatedBy: "system", UpdatedBy: "system", CreatedAt: testNow, UpdatedAt: testNow,
	}
	if err := repos.Items.Create(context.Background(), item); err != nil {
		t.Fatalf("seed sensitive item: %v", err)
	}
	version := domain.ItemVersion{
		ID: id + 1000, ItemID: item.ID, Version: 1, ValueText: value,
		ValueHash: domain.HashValue(value), ChangeSummary: "seed", CreatedBy: "system", CreatedAt: testNow,
	}
	if err := repos.Items.AppendVersion(context.Background(), version); err != nil {
		t.Fatalf("seed sensitive version: %v", err)
	}
	return item.ID
}

func TestAdminAPIAndSDKIntegration(t *testing.T) {
	server, repos, verifier := newIntegrationServer(t)
	base := server.URL
	ctx := context.Background()

	// 1. 管理面创建键并把值更新到版本 2。
	status, created := do(t, http.MethodPost, base+"/admin/v1/configs/items", "u1",
		[]string{"config:read", "config:write"},
		map[string]any{"env": "dev", "namespace": "platform", "key": "log.level",
			"value": "info", "owner": "team-platform"})
	if status != http.StatusCreated {
		t.Fatalf("create item status = %d (%s)", status, created.Message)
	}
	item := decodeData[itemDTO](t, created)
	status, updated := do(t, http.MethodPut,
		base+"/admin/v1/configs/items/"+strconv.FormatInt(item.ID, 10), "u1",
		[]string{"config:write"},
		map[string]any{"value": "debug", "change_summary": "debug", "expected_version": 1})
	if status != http.StatusOK {
		t.Fatalf("update item status = %d (%s)", status, updated.Message)
	}
	if got := decodeData[itemDTO](t, updated).CurrentVersion; got != 2 {
		t.Fatalf("current_version = %d, want 2", got)
	}

	// 2. 敏感项直接播种，并与普通键一起进入发布单。
	sensitiveID := seedSensitiveItem(t, repos, 2000, "smtp.password", "p@ssw0rd")

	createRelease := func(name string, itemVersion int64) releaseDTO {
		t.Helper()
		status, envelope := do(t, http.MethodPost, base+"/admin/v1/configs/releases", "u1",
			[]string{"config:write"},
			map[string]any{"env": "dev", "namespace": "platform", "name": name, "strategy": "all",
				"entries": []map[string]any{
					{"item_id": item.ID, "item_version": itemVersion},
					{"item_id": sensitiveID, "item_version": 1},
				}})
		if status != http.StatusCreated {
			t.Fatalf("create release %s status = %d (%s)", name, status, envelope.Message)
		}
		return decodeData[releaseDTO](t, envelope)
	}
	submit := func(releaseID int64) {
		t.Helper()
		status, envelope := do(t, http.MethodPost,
			base+"/admin/v1/configs/releases/"+strconv.FormatInt(releaseID, 10)+"/submit", "u1",
			[]string{"config:write"}, map[string]any{})
		if status != http.StatusOK {
			t.Fatalf("submit release status = %d (%s)", status, envelope.Message)
		}
	}
	approve := func(releaseID int64) {
		t.Helper()
		status, envelope := do(t, http.MethodPost,
			base+"/admin/v1/configs/releases/"+strconv.FormatInt(releaseID, 10)+"/approve", "u2",
			[]string{"config:approve"}, map[string]any{"decision": "approved", "comment": "ok"})
		if status != http.StatusOK {
			t.Fatalf("approve release status = %d (%s)", status, envelope.Message)
		}
	}
	publish := func(releaseID int64) releaseDTO {
		t.Helper()
		status, envelope := do(t, http.MethodPost,
			base+"/admin/v1/configs/releases/"+strconv.FormatInt(releaseID, 10)+"/publish", "u3",
			[]string{"config:publish"}, map[string]any{})
		if status != http.StatusOK {
			t.Fatalf("publish release status = %d (%s)", status, envelope.Message)
		}
		return decodeData[releaseDTO](t, envelope)
	}

	releaseA := createRelease("首个发布", 2)
	submit(releaseA.ID)
	approve(releaseA.ID)
	publishedA := publish(releaseA.ID)
	if publishedA.Status != string(domain.ReleaseStatusActive) {
		t.Fatalf("release A status = %q, want active", publishedA.Status)
	}
	if publishedA.SnapshotKeyID != "k1" || publishedA.SnapshotSignature == "" {
		t.Fatalf("publish must return signature: %+v", publishedA)
	}

	// 3. SDK 从同一仓储拉取快照并验签；敏感值不得进入取值集合。
	snapshots, err := app.NewSnapshotService(repos.Items, repos.Releases, repos.Pointers)
	if err != nil {
		t.Fatalf("new snapshot service: %v", err)
	}
	snapshotPath := filepath.Join(t.TempDir(), "snapshot.json")
	store, err := configclient.NewFileStore(snapshotPath)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	client, err := configclient.NewClient(configclient.ClientConfig{
		Source: snapshots, Verifier: verifier, Env: "dev", Namespace: "platform", Store: store,
	})
	if err != nil {
		t.Fatalf("new sdk client: %v", err)
	}
	if err := client.Refresh(ctx); err != nil {
		t.Fatalf("sdk refresh: %v", err)
	}
	if got := client.String("log.level", ""); got != "debug" {
		t.Fatalf("sdk value = %q, want debug", got)
	}
	snapshot, ok := client.Snapshot()
	if !ok || snapshot.ReleaseID != publishedA.ID {
		t.Fatalf("sdk snapshot release = %+v, want %d", snapshot, publishedA.ID)
	}
	if _, ok := client.Values()["smtp.password"]; ok {
		t.Fatal("sensitive value must not be exposed to sdk")
	}
	if got := len(snapshot.Items); got != 2 {
		t.Fatalf("snapshot items = %d, want 2", got)
	}

	// 4. 权限边界：读权限不能发布，未认证请求被拒。
	publishPath := base + "/admin/v1/configs/releases/" + strconv.FormatInt(releaseA.ID, 10) + "/publish"
	if status, _ := do(t, http.MethodPost, publishPath, "u1", []string{"config:read"}, map[string]any{}); status != http.StatusForbidden {
		t.Fatalf("publish without permission status = %d, want 403", status)
	}
	if status, _ := do(t, http.MethodGet, base+"/admin/v1/configs/namespaces", "", nil, nil); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", status)
	}

	// 5. 第二次发布后 SDK 重新拉取，取值随之收敛。
	status, updated = do(t, http.MethodPut,
		base+"/admin/v1/configs/items/"+strconv.FormatInt(item.ID, 10), "u1",
		[]string{"config:write"},
		map[string]any{"value": "warn", "change_summary": "warn", "expected_version": 2})
	if status != http.StatusOK {
		t.Fatalf("second update status = %d (%s)", status, updated.Message)
	}
	releaseB := createRelease("第二个发布", 3)
	submit(releaseB.ID)
	approve(releaseB.ID)
	publish(releaseB.ID)
	if err := client.Refresh(ctx); err != nil {
		t.Fatalf("sdk refresh after B: %v", err)
	}
	if got := client.String("log.level", ""); got != "warn" {
		t.Fatalf("sdk value after B = %q, want warn", got)
	}

	// 6. 回滚到 A：SDK 再次拉取应回到 A 的取值。
	status, rolledBack := do(t, http.MethodPost,
		base+"/admin/v1/configs/releases/"+strconv.FormatInt(releaseA.ID, 10)+"/rollback", "u3",
		[]string{"config:publish"}, map[string]any{"comment": "rollback to A"})
	if status != http.StatusOK {
		t.Fatalf("rollback status = %d (%s)", status, rolledBack.Message)
	}
	if err := client.Refresh(ctx); err != nil {
		t.Fatalf("sdk refresh after rollback: %v", err)
	}
	if got := client.String("log.level", ""); got != "debug" {
		t.Fatalf("sdk value after rollback = %q, want debug", got)
	}

	// 7. 冷启动回退：中心不可用时从本地快照恢复并验签。
	offline, err := configclient.NewClient(configclient.ClientConfig{
		Source: failingSource{}, Verifier: verifier, Env: "dev", Namespace: "platform", Store: store,
	})
	if err != nil {
		t.Fatalf("new offline client: %v", err)
	}
	if err := offline.Refresh(ctx); err == nil {
		t.Fatal("offline refresh must report source failure")
	}
	if got := offline.String("log.level", ""); got != "debug" {
		t.Fatalf("offline value = %q, want debug", got)
	}
	if !offline.Degraded() {
		t.Fatal("offline client must be degraded")
	}
}

// failingSource 模拟中心不可用。
type failingSource struct{}

func (failingSource) Load(context.Context, string, string) (configclient.Snapshot, error) {
	return configclient.Snapshot{}, context.DeadlineExceeded
}
