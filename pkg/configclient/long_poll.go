package configclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Notifier 在配置变更时通知客户端刷新。
type Notifier interface {
	Wait(ctx context.Context, env string, versions map[string]int64) ([]string, error)
}

// LongPoller 调用 config-api 通知端点。
type LongPoller struct {
	baseURL         string
	serviceIdentity string
	http            *http.Client
	timeout         time.Duration
}

// LongPollerConfig 是 LongPoller 装配参数。
type LongPollerConfig struct {
	BaseURL         string
	ServiceIdentity string
	HTTPClient      *http.Client
	Timeout         time.Duration
}

// NewLongPoller 构造长轮询器。
func NewLongPoller(cfg LongPollerConfig) (*LongPoller, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	identity := strings.TrimSpace(cfg.ServiceIdentity)
	if base == "" || identity == "" {
		return nil, fmt.Errorf("configclient: long poller requires base url and service identity")
	}
	client := cfg.HTTPClient
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if client == nil {
		// 客户端超时略长于服务端挂起，避免误杀。
		client = &http.Client{Timeout: timeout + 5*time.Second}
	}
	return &LongPoller{
		baseURL: base, serviceIdentity: identity, http: client, timeout: timeout,
	}, nil
}

type notifyEnvelope struct {
	Code int `json:"code"`
	Data struct {
		Items []struct {
			Namespace      string `json:"namespace"`
			NotificationID string `json:"notification_id"`
		} `json:"items"`
	} `json:"data"`
	Message string `json:"message"`
}

// Wait 挂起直到任一 namespace 版本变化；返回变化的 namespace 列表。
func (p *LongPoller) Wait(ctx context.Context, env string, versions map[string]int64) ([]string, error) {
	if len(versions) == 0 {
		return nil, fmt.Errorf("configclient: long poll requires versions")
	}
	parts := make([]string, 0, len(versions))
	for ns, ver := range versions {
		parts = append(parts, ns+":"+strconv.FormatInt(ver, 10))
	}
	q := url.Values{}
	q.Set("env", env)
	q.Set("namespaces", strings.Join(parts, ","))
	q.Set("timeout", p.timeout.String())
	endpoint := p.baseURL + "/internal/v1/configs/notifications?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Service-Identity", p.serviceIdentity)
	req.Header.Set("Accept", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("configclient: notifications: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("configclient: notifications status %d: %s",
			resp.StatusCode, truncate(string(body), 200))
	}
	var envelope notifyEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("configclient: decode notifications: %w", err)
	}
	out := make([]string, 0, len(envelope.Data.Items))
	for _, item := range envelope.Data.Items {
		if item.Namespace != "" {
			out = append(out, item.Namespace)
		}
	}
	return out, nil
}
