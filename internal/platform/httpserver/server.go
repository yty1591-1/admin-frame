package httpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform/config"
)

// newHTTPServer 按配置构造加固后的 http.Server（REL-02 / OBS-01）。
//
// BaseContext 继承进程 ctx，进程取消时所有在途请求的 context 一并失效；
// ErrorLog 接到 slog，避免标准库错误脱离结构化日志。
func newHTTPServer(ctx context.Context, cfg *config.Config, handler http.Handler, log *slog.Logger, tlsCfg *tls.Config) *http.Server {
	if log == nil {
		log = slog.Default()
	}
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		MaxHeaderBytes:    cfg.HTTP.MaxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
}

// ListenAndServe 启动 HTTP 服务，并在 ctx 取消或 SIGINT/SIGTERM 时优雅关闭。
//
// 先绑定端口再打印 listening 日志：绑定失败直接返回错误，不会留下
// “日志显示已监听、实际未监听”的误导信息。tlsCfg 非空时以 TLS 接受连接。
func ListenAndServe(ctx context.Context, cfg *config.Config, handler http.Handler, log *slog.Logger, tlsCfg ...*tls.Config) error {
	if cfg == nil {
		return errors.New("platform: nil config")
	}
	var tlsConfig *tls.Config
	if len(tlsCfg) > 0 {
		tlsConfig = tlsCfg[0]
	}
	srv := newHTTPServer(ctx, cfg, handler, log, tlsConfig)

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Addr, err)
	}
	if tlsConfig != nil {
		ln = tls.NewListener(ln, tlsConfig)
		if log != nil {
			log.Info("tls enabled", "client_auth", tlsConfig.ClientAuth.String())
		}
	}
	return serve(ctx, srv, ln, cfg.ShutdownTimeout, log)
}

// serve 运行已绑定的 Server，并在退出信号或 ctx 取消时排空在途请求。
func serve(ctx context.Context, srv *http.Server, ln net.Listener, shutdownTimeout time.Duration, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	errCh := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	log.Info("server listening", "addr", ln.Addr().String())

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-sigCtx.Done():
		log.Info("shutdown requested")
	case err := <-errCh:
		return err
	}

	if shutdownTimeout <= 0 {
		shutdownTimeout = 15 * time.Second
	}
	shCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errCh; err != nil {
		return err
	}
	log.Info("server stopped")
	return nil
}
