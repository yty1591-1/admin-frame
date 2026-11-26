package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/admin-frame/admin-frame/cmd/internal/configboot"
)

// snapshotConfigAPI 在 fakeConfigAPI 的基础上补出配置快照与自愈重置两个端点，
// 用于覆盖 admin-api 启动期「诊断 → 重置 → 重载」的整条重试链路：真实走
// configboot.Load 的 HTTP 拉取与 configboot.ResetInvalidValues 的上报。
type snapshotConfigAPI struct {
	mu     sync.Mutex
	values map[string]string
	// level 是 log.level 的声明默认值：重置时把它写回快照，模拟 config-api 生效。
	level string

	resetRefs []string
	resetFail bool
	identity  string
	pulls     int
}

func (f *snapshotConfigAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/internal/v1/install/status":
		writeJSON(w, http.StatusOK, map[string]any{
			"code": 0,
			"data": map[string]any{"env": "dev", "status": "ready"},
		})
	case r.URL.Path == "/internal/v1/configs/repair/reset-defaults":
		f.handleReset(w, r)
	case strings.HasPrefix(r.URL.Path, "/internal/v1/configs/"):
		f.handleSnapshot(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *snapshotConfigAPI) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	namespace := strings.TrimPrefix(r.URL.Path, "/internal/v1/configs/dev/")
	f.mu.Lock()
	f.pulls++
	values := make(map[string]string, len(f.values))
	for key, value := range f.values {
		values[key] = value
	}
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"code": 0,
		"data": map[string]any{
			"env": "dev", "namespace": namespace,
			"release_id": 1, "release_no": 1, "pointer_version": 1,
			"release_key": "1:1", "values": values,
		},
	})
}

func (f *snapshotConfigAPI) handleReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Env  string   `json:"env"`
		Refs []string `json:"refs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.identity = r.Header.Get("X-Service-Identity")
	f.resetRefs = append([]string(nil), body.Refs...)
	if f.resetFail {
		writeJSON(w, http.StatusOK, map[string]any{"code": 50000, "message": "kek unavailable"})
		return
	}
	for _, ref := range body.Refs {
		if ref == "platform/log.level" {
			f.values["log.level"] = f.level
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code": 0,
		"data": map[string]any{
			"reset": []map[string]string{
				{"namespace": "platform", "key": "log.level", "default_source": "code_default"},
			},
			"skipped":      []map[string]string{},
			"release_keys": map[string]string{"platform": "2:1"},
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// setConfigAPIEnv 让 configboot.Load 指向测试桩：单命名空间即可覆盖拉取链路。
func setConfigAPIEnv(t *testing.T, baseURL string) {
	t.Helper()
	t.Setenv("CONFIG_API_URL", baseURL)
	t.Setenv("CONFIG_SERVICE_IDENTITY", "admin-api")
	t.Setenv("CONFIGCENTER_ENV", "dev")
	t.Setenv("CONFIGCENTER_NAMESPACES", "platform")
	t.Setenv("CONFIGCENTER_CACHE_DIR", t.TempDir())
	t.Setenv("CONFIG_API_MTLS", "off")
}

func specDefaults(t *testing.T) map[string]string {
	t.Helper()
	values, err := configboot.BuildSpecDefaultValues()
	if err != nil {
		t.Fatalf("build spec defaults: %v", err)
	}
	if len(values) == 0 {
		t.Fatal("key spec 未提供任何默认值，夹具不可用")
	}
	return values
}

func cloneValues(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

// levelParse 用 log.level 的枚举语义模拟 pkg/config 的校验：只接受声明默认值。
func levelParse(valid string) func(map[string]string) error {
	return func(values map[string]string) error {
		if got := strings.TrimSpace(values["log.level"]); got != valid {
			return fmt.Errorf("config: invalid config: config validation failed: log.level: unsupported value %q", got)
		}
		return nil
	}
}

// driverParse 模拟「键存在但为空」的必填校验失败。
func driverParse(values map[string]string) error {
	if strings.TrimSpace(values["db.driver"]) == "" {
		return fmt.Errorf("config: invalid config: config validation failed: db.driver is required")
	}
	return nil
}

func TestHealStartupConfigResetsInvalidValuesAndReloads(t *testing.T) {
	defaults := specDefaults(t)
	level := defaults["log.level"]
	if level == "" {
		t.Fatal("log.level 缺少声明默认值，夹具不可用")
	}
	values := cloneValues(defaults)
	values["log.level"] = "verbose"

	api := &snapshotConfigAPI{values: values, level: level}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	setConfigAPIEnv(t, srv.URL)

	ctx := context.Background()
	parse := levelParse(level)
	boot, err := configboot.Load(ctx)
	if err != nil {
		t.Fatalf("configboot.Load: %v", err)
	}
	cause := parse(boot.Values)
	if cause == nil {
		t.Fatalf("夹具应校验失败，实际 log.level=%q 通过", boot.Values["log.level"])
	}

	heal := healStartupConfig(ctx, boot, boot.Values, cause, parse, configboot.Load)
	if len(heal.Gaps) != 0 {
		t.Fatalf("值非法不应被判为缺值: %v", heal.Gaps)
	}
	if !heal.Reloaded {
		t.Fatal("重置成功后应完成重载")
	}
	if want := []string{"platform/log.level"}; !reflect.DeepEqual(heal.ResetRefs, want) {
		t.Fatalf("重置键集合 = %v, want %v", heal.ResetRefs, want)
	}
	if err := parse(heal.Boot.Values); err != nil {
		t.Fatalf("重载后仍不可用: %v", err)
	}

	api.mu.Lock()
	refs, identity, pulls := api.resetRefs, api.identity, api.pulls
	api.mu.Unlock()
	if want := []string{"platform/log.level"}; !reflect.DeepEqual(refs, want) {
		t.Fatalf("上报给 config-api 的键 = %v, want %v", refs, want)
	}
	if identity != "admin-api" {
		t.Fatalf("自愈请求必须携带服务身份，got %q", identity)
	}
	if pulls != 2 {
		t.Fatalf("期望拉取两次（初始 + 重载），实际 %d", pulls)
	}
}

func TestHealStartupConfigReportsMissingValues(t *testing.T) {
	defaults := specDefaults(t)
	values := cloneValues(defaults)
	delete(values, "db.driver")

	api := &snapshotConfigAPI{values: values, level: defaults["log.level"]}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	setConfigAPIEnv(t, srv.URL)

	ctx := context.Background()
	boot, err := configboot.Load(ctx)
	if err != nil {
		t.Fatalf("configboot.Load: %v", err)
	}
	cause := driverParse(boot.Values)
	if cause == nil {
		t.Fatal("夹具应因缺 db.driver 失败")
	}

	heal := healStartupConfig(ctx, boot, boot.Values, cause, driverParse, configboot.Load)
	if len(heal.Gaps) == 0 {
		t.Fatal("填占位值可通过校验时应判为缺值")
	}
	if !contains(heal.Gaps, "platform/db.driver") {
		t.Fatalf("缺值集合应包含 platform/db.driver，got %v", heal.Gaps)
	}
	if heal.Reloaded || len(heal.ResetRefs) != 0 {
		t.Fatalf("缺值路径不应触发重置: reloaded=%v refs=%v", heal.Reloaded, heal.ResetRefs)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.resetRefs) != 0 {
		t.Fatalf("缺值路径不应调用自愈端点，got %v", api.resetRefs)
	}
}

func TestHealStartupConfigKeepsFailFastWhenResetFails(t *testing.T) {
	defaults := specDefaults(t)
	values := cloneValues(defaults)
	values["log.level"] = "verbose"

	api := &snapshotConfigAPI{values: values, level: defaults["log.level"], resetFail: true}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	setConfigAPIEnv(t, srv.URL)

	ctx := context.Background()
	parse := levelParse(defaults["log.level"])
	boot, err := configboot.Load(ctx)
	if err != nil {
		t.Fatalf("configboot.Load: %v", err)
	}
	cause := parse(boot.Values)

	heal := healStartupConfig(ctx, boot, boot.Values, cause, parse, configboot.Load)
	if heal.Reloaded {
		t.Fatal("重置失败时不得声称已重载")
	}
	if want := []string{"platform/log.level"}; !reflect.DeepEqual(heal.ResetRefs, want) {
		t.Fatalf("仍应记录诊断出的键，got %v", heal.ResetRefs)
	}
	api.mu.Lock()
	pulls := api.pulls
	api.mu.Unlock()
	if pulls != 1 {
		t.Fatalf("重置失败后不应重载，期望拉取 1 次，实际 %d", pulls)
	}
}

func TestHealStartupConfigStaysFailFastWhenUndiagnosable(t *testing.T) {
	defaults := specDefaults(t)
	api := &snapshotConfigAPI{values: cloneValues(defaults), level: defaults["log.level"]}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	setConfigAPIEnv(t, srv.URL)

	ctx := context.Background()
	// 文案里没有键名、重置任何单键也救不回来：判不出就保持 fail-fast。
	parse := func(map[string]string) error {
		return fmt.Errorf("config: invalid config: config validation failed: unsupported combination of storage settings")
	}
	boot, err := configboot.Load(ctx)
	if err != nil {
		t.Fatalf("configboot.Load: %v", err)
	}

	heal := healStartupConfig(ctx, boot, boot.Values, parse(boot.Values), parse, configboot.Load)
	if len(heal.Gaps) != 0 || len(heal.ResetRefs) != 0 || heal.Reloaded {
		t.Fatalf("判不出时不应做任何动作: %+v", heal)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.resetRefs) != 0 {
		t.Fatalf("判不出时不应调用自愈端点，got %v", api.resetRefs)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
