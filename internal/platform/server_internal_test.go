package platform

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestNewHTTPServerHardening 验证 Server 超时、ErrorLog 与 BaseContext（REL-02）。
func TestNewHTTPServerHardening(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{ k string }{"test"}, "v")
	cfg := &Config{
		Addr: "127.0.0.1:0",
		HTTP: HTTPConfig{
			ReadHeaderTimeout: time.Second,
			ReadTimeout:       2 * time.Second,
			WriteTimeout:      3 * time.Second,
			IdleTimeout:       4 * time.Second,
			MaxHeaderBytes:    2048,
		},
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	srv := newHTTPServer(ctx, cfg, http.NewServeMux(), logger)

	if srv.ReadHeaderTimeout != time.Second || srv.ReadTimeout != 2*time.Second ||
		srv.WriteTimeout != 3*time.Second || srv.IdleTimeout != 4*time.Second {
		t.Fatalf("timeouts not wired: %+v", srv)
	}
	if srv.MaxHeaderBytes != 2048 {
		t.Fatalf("max header bytes=%d", srv.MaxHeaderBytes)
	}
	if srv.ErrorLog == nil {
		t.Fatal("ErrorLog must be wired to slog")
	}
	if srv.BaseContext == nil {
		t.Fatal("BaseContext must be set")
	}
	if got := srv.BaseContext(nil); got != ctx {
		t.Fatal("BaseContext must inherit process context")
	}
}

// TestListenAndServeRejectsNilConfig 验证 nil 配置快速失败。
func TestListenAndServeRejectsNilConfig(t *testing.T) {
	if err := ListenAndServe(context.Background(), nil, http.NewServeMux(), nil); err == nil {
		t.Fatal("expected error for nil config")
	}
}

// TestListenAndServeBindError 验证端口占用/非法地址不会被吞掉。
func TestListenAndServeBindError(t *testing.T) {
	cfg := &Config{Addr: "127.0.0.1:99999", ShutdownTimeout: time.Second}
	if err := ListenAndServe(context.Background(), cfg, http.NewServeMux(), nil); err == nil {
		t.Fatal("expected listen error")
	}
}

// TestListenAndServeShutdownOnContext 验证 ctx 取消触发优雅关闭且返回 nil。
func TestListenAndServeShutdownOnContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := &Config{
		Addr:            "127.0.0.1:0",
		ShutdownTimeout: time.Second,
		HTTP:            HTTPConfig{ReadHeaderTimeout: time.Second},
	}
	if err := ListenAndServe(ctx, cfg, http.NewServeMux(), nil); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

// TestServeReturnsOnListenerClose 验证监听器异常关闭时 serve 返回错误。
func TestServeReturnsOnListenerClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Addr: ln.Addr().String(), ShutdownTimeout: time.Second}
	srv := newHTTPServer(context.Background(), cfg, http.NewServeMux(), nil)
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if err := serve(context.Background(), srv, ln, time.Second, nil); err == nil {
		t.Fatal("expected error after listener closed")
	}
}
