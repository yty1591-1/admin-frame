package secretruntime

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
)

// LoadServerTLSFromEnv 按环境变量装配 config-api TLS / mTLS（env 未声明时不强制）。
//
// 环境变量：
//   - CONFIG_API_TLS_CERT / CONFIG_API_TLS_KEY：服务端证书
//   - CONFIG_API_TLS_CLIENT_CA：客户端 CA（启用后按 CONFIG_API_MTLS 决定校验强度）
//   - CONFIG_API_MTLS：off | optional | require（默认 off；staging/prod 必须 require）
//
// 未配置证书且非 prod-like 时返回 (nil, nil)，保持本机明文演练能力。
func LoadServerTLSFromEnv() (*tls.Config, error) {
	return LoadServerTLSFromEnvFor(strings.TrimSpace(os.Getenv("CONFIGCENTER_ENV")))
}

// LoadServerTLSFromEnvFor 与 LoadServerTLSFromEnv 相同，但使用显式部署环境（CC-R-04）。
func LoadServerTLSFromEnvFor(env string) (*tls.Config, error) {
	certFile := strings.TrimSpace(os.Getenv("CONFIG_API_TLS_CERT"))
	keyFile := strings.TrimSpace(os.Getenv("CONFIG_API_TLS_KEY"))
	caFile := strings.TrimSpace(os.Getenv("CONFIG_API_TLS_CLIENT_CA"))
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("CONFIG_API_MTLS")))
	if mode == "" {
		mode = "off"
	}

	if platformconfig.IsProdLikeEnv(env) {
		if mode != "require" {
			return nil, fmt.Errorf(
				"secretruntime: CONFIG_API_MTLS must be require when env=%s (got %q); CC-R-04",
				strings.TrimSpace(env), mode,
			)
		}
		if certFile == "" || keyFile == "" {
			return nil, fmt.Errorf(
				"secretruntime: CONFIG_API_TLS_CERT/KEY required when env=%s and mTLS=require",
				strings.TrimSpace(env),
			)
		}
		if caFile == "" {
			return nil, fmt.Errorf(
				"secretruntime: CONFIG_API_TLS_CLIENT_CA required when env=%s and mTLS=require",
				strings.TrimSpace(env),
			)
		}
	}

	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("secretruntime: CONFIG_API_TLS_CERT and CONFIG_API_TLS_KEY must both be set")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("secretruntime: load server cert: %w", err)
	}
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile) //nolint:gosec // G304: 路径来自运维注入的 CONFIG_API_TLS_CLIENT_CA。
		if err != nil {
			return nil, fmt.Errorf("secretruntime: read client ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("secretruntime: parse client ca")
		}
		cfg.ClientCAs = pool
		switch mode {
		case "require":
			cfg.ClientAuth = tls.RequireAndVerifyClientCert
		case "optional":
			cfg.ClientAuth = tls.VerifyClientCertIfGiven
		default:
			cfg.ClientAuth = tls.NoClientCert
		}
	} else if mode == "require" {
		return nil, fmt.Errorf("secretruntime: CONFIG_API_MTLS=require requires CONFIG_API_TLS_CLIENT_CA")
	}
	return cfg, nil
}

// LoadClientHTTPFromEnv 构造访问 config-api 的 HTTP 客户端（可选 mTLS）。
//
// 环境变量：
//   - CONFIG_API_CLIENT_CERT / CONFIG_API_CLIENT_KEY：客户端证书
//   - CONFIG_API_TLS_CA：信任服务端的 CA（与服务端 CONFIG_API_TLS_CERT 对应）
//
// prod-like 且 CONFIG_API_MTLS=require 时三者必填；其它环境未配置则返回默认超时客户端。
func LoadClientHTTPFromEnv() (*http.Client, error) {
	return LoadClientHTTPFromEnvFor(strings.TrimSpace(os.Getenv("CONFIGCENTER_ENV")))
}

// LoadClientHTTPFromEnvFor 与 LoadClientHTTPFromEnv 相同，但使用显式部署环境。
func LoadClientHTTPFromEnvFor(env string) (*http.Client, error) {
	certFile := strings.TrimSpace(os.Getenv("CONFIG_API_CLIENT_CERT"))
	keyFile := strings.TrimSpace(os.Getenv("CONFIG_API_CLIENT_KEY"))
	caFile := strings.TrimSpace(os.Getenv("CONFIG_API_TLS_CA"))
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("CONFIG_API_MTLS")))
	if mode == "" {
		mode = "off"
	}

	needMTLS := platformconfig.IsProdLikeEnv(env) && mode == "require"
	if needMTLS {
		if certFile == "" || keyFile == "" {
			return nil, fmt.Errorf(
				"secretruntime: CONFIG_API_CLIENT_CERT/KEY required when env=%s and mTLS=require",
				strings.TrimSpace(env),
			)
		}
		if caFile == "" {
			return nil, fmt.Errorf(
				"secretruntime: CONFIG_API_TLS_CA required when env=%s and mTLS=require",
				strings.TrimSpace(env),
			)
		}
	}
	if certFile == "" && keyFile == "" && caFile == "" {
		return &http.Client{Timeout: 5 * time.Second}, nil
	}
	if (certFile == "") != (keyFile == "") {
		return nil, fmt.Errorf("secretruntime: CONFIG_API_CLIENT_CERT and CONFIG_API_CLIENT_KEY must both be set")
	}

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if certFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("secretruntime: load client cert: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile) //nolint:gosec // G304: 路径来自运维注入的 CONFIG_API_TLS_CA。
		if err != nil {
			return nil, fmt.Errorf("secretruntime: read tls ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("secretruntime: parse tls ca")
		}
		tlsCfg.RootCAs = pool
	}

	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}, nil
}
