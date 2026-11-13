package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type recordingObserver struct {
	route   string
	status  int
	elapsed time.Duration
	calls   int
}

func (r *recordingObserver) ObserveAdminRequest(route string, status int, elapsed time.Duration) {
	r.route = route
	r.status = status
	r.elapsed = elapsed
	r.calls++
}

// TestObserveRecordsStatus 验证观测中间件捕获真实状态码且不吞掉响应。
func TestObserveRecordsStatus(t *testing.T) {
	observer := &recordingObserver{}
	handler := observe(observer, "releases.publish", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/admin/v1/configs/releases/1/publish", nil))

	if recorder.Code != http.StatusConflict {
		t.Fatalf("response status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	if observer.calls != 1 || observer.route != "releases.publish" || observer.status != http.StatusConflict {
		t.Fatalf("observer = %+v, want one conflict on releases.publish", observer)
	}
	if observer.elapsed < 0 {
		t.Fatalf("elapsed = %v, want >= 0", observer.elapsed)
	}
}

// TestObserveNilObserverPassthrough 验证未装配观测时行为不变。
func TestObserveNilObserverPassthrough(t *testing.T) {
	var observer RequestObserver
	if observer != nil {
		t.Fatal("nil observer must stay nil")
	}
	mux := http.NewServeMux()
	handle(mux, observer, "items.list", "GET /admin/v1/configs/items", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/v1/configs/items", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("response status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}
