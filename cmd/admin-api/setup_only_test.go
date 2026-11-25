package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/cmd/internal/configboot"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
)

// fakeConfigAPI 模拟 config-api 的安装态与向导代理上游。
func fakeConfigAPI(t *testing.T, status string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/internal/v1/install/status":
			body, _ := json.Marshal(map[string]any{
				"code": 0,
				"data": map[string]any{"env": "dev", "status": status},
			})
			_, _ = w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestRunSetupOnlyServesWizardAndReadiness(t *testing.T) {
	upstream := fakeConfigAPI(t, "pending")
	defer upstream.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	t.Setenv("ADMIN_API_ADDR", addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	boot := &configboot.Result{Env: "dev", ConfigAPIURL: upstream.URL, HTTPClient: upstream.Client()}
	build := httpserver.BuildInfo{Service: "admin-api", Version: "test"}

	errCh := make(chan error, 1)
	go func() {
		errCh <- runSetupOnly(ctx, boot, []string{"platform/auth.jwt.issuer"}, build)
	}()

	base := "http://" + addr
	waitFor(t, base+"/readyz", func(resp *http.Response, body string) bool {
		return resp.StatusCode == http.StatusServiceUnavailable && strings.Contains(body, "config_incomplete")
	})

	// 向导代理可用：status 透传到 config-api。
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(base + "/admin/v1/install/status") //nolint:noctx // 测试内短连接。
	if err != nil {
		t.Fatalf("call install proxy: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"pending"`) {
		t.Fatalf("install proxy must forward status, got %d %s", resp.StatusCode, body)
	}

	// 业务路由不暴露。
	resp, err = client.Get(base + "/admin/v1/configs/namespaces") //nolint:noctx // 测试内短连接。
	if err != nil {
		t.Fatalf("call business route: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("business routes must stay unmounted, got %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runSetupOnly returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runSetupOnly did not shut down")
	}
}

func waitFor(t *testing.T, url string, ok func(*http.Response, string) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	client := &http.Client{Timeout: time.Second}
	var last string
	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:noctx // 测试轮询使用带超时的客户端。
		if err != nil {
			last = err.Error()
			time.Sleep(20 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if ok(resp, string(body)) {
			return
		}
		last = resp.Status + " " + string(body)
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s not ready, last: %s", url, last)
}
