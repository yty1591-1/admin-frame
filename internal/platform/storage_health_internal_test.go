package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/health"
)

// TestRegisterStorageChecksProbesDefaultAndNamedBuckets 验证就绪探针覆盖默认桶与命名桶。
//
// 命名桶漏注册会让该桶不可用时进程仍被视为 Ready，异步导出与审计归档会把文件
// 写到不可用后端（OPS-02）。
func TestRegisterStorageChecksProbesDefaultAndNamedBuckets(t *testing.T) {
	stores, err := OpenStorageSet(context.Background(), StorageConfig{
		Driver: "memory",
		Buckets: map[string]BucketConfig{
			"exports": {Driver: "memory"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// 忽略：测试清理阶段关闭存储集合，关闭失败不影响断言。
		_ = stores.Close()
	})

	hh := health.New()
	registerStorageChecks(hh, stores)

	rec := httptest.NewRecorder()
	hh.Readiness()(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, name := range []string{`"storage"`, `"storage:exports"`} {
		if !strings.Contains(body, name) {
			t.Fatalf("readiness output missing %s: %s", name, body)
		}
	}
}
