package configboot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/health"
)

// InstallPendingJWTSecret 仅供安装未完成时让业务进程通过 Validate；非业务签发密钥。
const InstallPendingJWTSecret = "install-pending-process-jwt-secret-32b"

// InstallSetupTokenHeader 与 config-api 写路径鉴权头一致（ADR-0017）。
const InstallSetupTokenHeader = "X-Config-Install-Token" //nolint:gosec // G101: 请求头名常量，非凭据。

// InstallStatus 是 config-api 安装态查询结果。
type InstallStatus struct {
	Env    string
	Status string
}

// FetchInstallStatus 查询 GET /internal/v1/install/status。
func FetchInstallStatus(ctx context.Context, baseURL string, httpClient *http.Client) (InstallStatus, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return InstallStatus{}, fmt.Errorf("configboot: empty config-api url")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/internal/v1/install/status", http.NoBody) //nolint:gosec // G704: baseURL 来自受控环境变量 CONFIG_API_URL。
	if err != nil {
		return InstallStatus{}, err
	}
	resp, err := httpClient.Do(req) //nolint:gosec // G704: 同上，请求目标是受控的 config-api 内部地址。
	if err != nil {
		return InstallStatus{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return InstallStatus{}, fmt.Errorf("configboot: install status http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var envelope struct {
		Data struct {
			Env    string `json:"env"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return InstallStatus{}, fmt.Errorf("configboot: decode install status: %w", err)
	}
	return InstallStatus{Env: envelope.Data.Env, Status: envelope.Data.Status}, nil
}

// IsInstallReady 报告 status 是否为 ready。
func IsInstallReady(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "ready")
}

// BuildSpecDefaultValues 从 key spec 默认值构造启动 Values（安装未完成时的进程监听兜底）。
func BuildSpecDefaultValues() (map[string]string, error) {
	registry, err := spec.Platform()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	for _, ns := range registry.Namespaces() {
		for _, keySpec := range registry.List(ns) {
			if keySpec.Sensitive || keySpec.Default == "" {
				continue
			}
			out[keySpec.Key] = keySpec.Default
		}
	}
	return out, nil
}

// RegisterInstallReadyCheck 注册业务进程 /readyz 的 config_install 检查。
//
// 未 ready 时返回 50351 语义错误，使 /readyz 失败；短缓存 ≤5s。
func RegisterInstallReadyCheck(h *health.Handler, baseURL string, httpClient *http.Client) {
	if h == nil {
		return
	}
	var (
		mu       sync.Mutex
		cached   InstallStatus
		cachedAt time.Time
	)
	h.Register("config_install", func() error {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(cachedAt) < 5*time.Second && cached.Status != "" {
			if !IsInstallReady(cached.Status) {
				return apperr.New(apperr.CodeInstallRequired,
					"config center install not ready: "+cached.Status, nil)
			}
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		status, err := FetchInstallStatus(ctx, baseURL, httpClient)
		if err != nil {
			return apperr.New(apperr.CodeInstallRequired,
				"config center install status unavailable", err)
		}
		cached = status
		cachedAt = time.Now()
		if !IsInstallReady(status.Status) {
			return apperr.New(apperr.CodeInstallRequired,
				"config center install not ready: "+status.Status, nil)
		}
		return nil
	})
}
