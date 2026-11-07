package httpclient_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/httpclient"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

type attemptRecorder struct {
	attempts int
	statuses []int
}

func (r *attemptRecorder) AttemptObserved(attempt, statusCode int, _ error) {
	r.attempts++
	r.statuses = append(r.statuses, statusCode)
}

func TestObserverRecordsEachAttempt(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	obs := &attemptRecorder{}
	client, err := httpclient.New(httpclient.Config{
		Timeout: 2 * time.Second,
		Retry: retry.NewPolicy(
			retry.WithMaxAttempts(2),
			retry.WithBackoff(time.Millisecond, 5*time.Millisecond),
		),
		Observer: obs,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if obs.attempts != 2 {
		t.Fatalf("attempts=%d want 2", obs.attempts)
	}
	if len(obs.statuses) != 2 || obs.statuses[0] != http.StatusServiceUnavailable || obs.statuses[1] != http.StatusOK {
		t.Fatalf("statuses=%v", obs.statuses)
	}
}
