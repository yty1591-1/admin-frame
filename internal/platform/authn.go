package platform

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// NewAuthMiddleware 按配置返回鉴权中间件（SEC-04）。
//
//	demo：仅允许 local / dev / test；prod 一律拒绝启动。
//	jwt：使用 pkg/jwt 校验签名、iss、aud、exp；API 进程只持有校验密钥。
//
// opts 用于注入撤销检查器（T5）；demo 模式忽略这些选项。
func NewAuthMiddleware(
	cfg AuthConfig,
	expected auth.Channel,
	env string,
	opts ...security.JWTAuthOption,
) (func(http.Handler) http.Handler, error) {
	if cfg.ExpectedChannel != "" && cfg.ExpectedChannel != string(expected) {
		return nil, fmt.Errorf("auth.expected_channel %q != process channel %q", cfg.ExpectedChannel, expected)
	}
	switch cfg.effectiveMode(env) {
	case AuthModeJWT:
		mw, err := security.NewJWTAuth(cfg.JWT, expected, opts...)
		if err != nil {
			return nil, fmt.Errorf("auth.jwt: %w", err)
		}
		return mw, nil
	case AuthModeDemo:
		if isProdEnv(env) {
			return nil, errors.New("auth.mode=demo is forbidden when env=prod")
		}
		return security.NewDemoAuth(expected), nil
	default:
		return nil, fmt.Errorf("unsupported auth.mode: %s", cfg.Mode)
	}
}
