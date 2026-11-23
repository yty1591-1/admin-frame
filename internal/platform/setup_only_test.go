package platform

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
)

func testSetupOnlyOptions() SetupOnlyOptions {
	return SetupOnlyOptions{
		Service:     "admin-api",
		Channel:     "admin",
		Env:         "dev",
		Addr:        ":0",
		Build:       httpserver.BuildInfo{Service: "admin-api", Version: "test"},
		MissingKeys: []string{"platform/http.base_url"},
		Mount: func(mux *http.ServeMux) {
			mux.HandleFunc("GET /admin/v1/install/status", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"code":0}`))
			})
		},
	}
}

func TestSetupOnlyMuxProbesReportMissingKeys(t *testing.T) {
	mux := newSetupOnlyMux(testSetupOnlyOptions(), "platform/http.base_url")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz must stay alive, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", http.NoBody))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz must fail while keys are missing, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "config_incomplete") || !strings.Contains(body, "platform/http.base_url") {
		t.Fatalf("/readyz must explain the missing keys, got %q", body)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/version", http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("/version must be served, got %d", rec.Code)
	}
}

func TestSetupOnlyMuxMountsWizardProxyOnly(t *testing.T) {
	mux := newSetupOnlyMux(testSetupOnlyOptions(), "platform/http.base_url")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/install/status", http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("install proxy route must be mounted, got %d", rec.Code)
	}

	// 降级态不暴露指标与业务路由。
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("/metrics must stay unmounted in setup-only mode, got %d", rec.Code)
	}
}

func TestWatchInstallReadyStopsProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		watchInstallReady(ctx, cancel, SetupOnlyOptions{
			PollInterval: time.Millisecond,
			Ready:        func(context.Context) (bool, error) { return true, nil },
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("install ready must stop the setup-only process")
	}
	<-done
}

func TestWatchInstallReadyStaysWhenAutoExitDisabled(t *testing.T) {
	t.Setenv(SetupOnlyAutoExitEnv, "false")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		watchInstallReady(ctx, cancel, SetupOnlyOptions{
			PollInterval: time.Millisecond,
			Ready:        func(context.Context) (bool, error) { return true, nil },
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()

	select {
	case <-ctx.Done():
		t.Fatal("auto exit disabled must keep serving")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	<-done
}

func TestRunSetupOnlyRejectsEmptyAddr(t *testing.T) {
	opts := testSetupOnlyOptions()
	opts.Addr = " "
	if err := RunSetupOnly(context.Background(), opts); err == nil {
		t.Fatal("empty addr must be rejected")
	}
}

func TestRunSetupOnlyServesThenStops(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := testSetupOnlyOptions()
	opts.Addr = addr
	opts.PollInterval = 10 * time.Millisecond
	opts.Ready = func(context.Context) (bool, error) { return false, nil }
	opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	errCh := make(chan error, 1)
	go func() { errCh <- RunSetupOnly(ctx, opts) }()

	url := "http://" + addr + "/readyz"
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		if reqErr != nil {
			t.Fatalf("build request: %v", reqErr)
		}
		resp, doErr := http.DefaultClient.Do(req)
		if doErr == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusServiceUnavailable && strings.Contains(string(body), "config_incomplete") {
				lastErr = nil
				break
			}
			lastErr = fmt.Errorf("status %d body %s", resp.StatusCode, body)
		} else {
			lastErr = doErr
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr != nil {
		cancel()
		t.Fatalf("setup-only server not reachable: %v", lastErr)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunSetupOnly returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunSetupOnly did not shut down")
	}
}
