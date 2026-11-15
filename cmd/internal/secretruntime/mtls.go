package secretruntime

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
)

// LoadServerTLSFromEnv 按环境变量装配 config-api TLS / mTLS。
//
// 环境变量：
//   - CONFIG_API_TLS_CERT / CONFIG_API_TLS_KEY：服务端证书
//   - CONFIG_API_TLS_CLIENT_CA：客户端 CA（启用后按 CONFIG_API_MTLS 决定校验强度）
//   - CONFIG_API_MTLS：off | optional | require（默认 off）
//
// 未配置证书时返回 (nil, nil)，保持本机明文演练能力。
func LoadServerTLSFromEnv() (*tls.Config, error) {
	certFile := strings.TrimSpace(os.Getenv("CONFIG_API_TLS_CERT"))
	keyFile := strings.TrimSpace(os.Getenv("CONFIG_API_TLS_KEY"))
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
	caFile := strings.TrimSpace(os.Getenv("CONFIG_API_TLS_CLIENT_CA"))
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("CONFIG_API_MTLS")))
	if mode == "" {
		mode = "off"
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
