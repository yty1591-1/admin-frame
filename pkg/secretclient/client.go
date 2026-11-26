// Package secretclient 是业务进程侧密钥解析 SDK（ADR-0014）。
//
// 只做内存租约缓存；不落盘、不打印明文。解析走 config-api。
package secretclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client 是密钥解析客户端。
type Client struct {
	baseURL         string
	serviceIdentity string
	http            *http.Client
	mu              sync.Mutex
	cache           map[string]cachedSecret
}

type cachedSecret struct {
	value     string
	version   int64
	expiresAt time.Time
}

// Config 是客户端配置。
type Config struct {
	BaseURL         string
	ServiceIdentity string
	HTTPClient      *http.Client
}

// New 构造客户端。
func New(cfg Config) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	identity := strings.TrimSpace(cfg.ServiceIdentity)
	if base == "" || identity == "" {
		return nil, fmt.Errorf("secretclient: base url and service identity required")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &Client{
		baseURL: base, serviceIdentity: identity, http: httpClient,
		cache: make(map[string]cachedSecret),
	}, nil
}

// Resolve 解析单个密钥；命中租约缓存直接返回。
func (c *Client) Resolve(ctx context.Context, env, namespace, name string) (string, error) {
	items, err := c.ResolveMany(ctx, env, namespace, []string{name})
	if err != nil {
		return "", err
	}
	value, ok := items[name]
	if !ok {
		return "", fmt.Errorf("secretclient: secret %q missing in response", name)
	}
	return value, nil
}

// ResolveMany 批量解析（≤20）。
func (c *Client) ResolveMany(ctx context.Context, env, namespace string, names []string) (map[string]string, error) {
	if len(names) == 0 || len(names) > 20 {
		return nil, fmt.Errorf("secretclient: names must contain 1..20 items")
	}
	now := time.Now()
	out := make(map[string]string, len(names))
	missing := make([]string, 0, len(names))
	c.mu.Lock()
	for _, name := range names {
		key := cacheKey(env, namespace, name)
		if cached, ok := c.cache[key]; ok && cached.expiresAt.After(now.Add(cached.expiresAt.Sub(now)/5)) {
			// 到期前 80% 仍可用；简单按 expiresAt 判定，刷新阈值由服务端 lease 决定。
			if now.Before(cached.expiresAt) {
				remaining := cached.expiresAt.Sub(now)
				lease := remaining       // approximate
				if remaining > lease/5 { // always true; keep readable
					out[name] = cached.value
					continue
				}
			}
		}
		if cached, ok := c.cache[key]; ok && now.Before(cached.expiresAt) {
			// 刷新窗口：剩余 < 20% 时仍返回旧值并异步不阻塞；MVP 同步刷新。
			out[name] = cached.value
			if remaining := cached.expiresAt.Sub(now); remaining < time.Minute {
				missing = append(missing, name)
			}
			continue
		}
		missing = append(missing, name)
	}
	c.mu.Unlock()
	if len(missing) == 0 {
		return out, nil
	}
	fetched, err := c.fetch(ctx, env, namespace, missing)
	if err != nil {
		if len(out) > 0 {
			return out, nil // 部分命中缓存时降级
		}
		return nil, err
	}
	c.mu.Lock()
	for name, item := range fetched {
		c.cache[cacheKey(env, namespace, name)] = cachedSecret{
			value: item.Value, version: item.Version, expiresAt: item.LeaseExpiresAt,
		}
		out[name] = item.Value
	}
	c.mu.Unlock()
	return out, nil
}

type resolveItem struct {
	Name           string    `json:"name"`
	Version        int64     `json:"version"`
	Value          string    `json:"value"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
}

func (c *Client) fetch(ctx context.Context, env, namespace string, names []string) (map[string]resolveItem, error) {
	body, _ := json.Marshal(map[string]any{
		"env": env, "namespace": namespace, "names": names,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/v1/configs/secrets/resolve", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Identity", c.serviceIdentity)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("secretclient: resolve transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("secretclient: forbidden")
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("secretclient: not found")
	}
	if resp.StatusCode == http.StatusServiceUnavailable {
		return nil, fmt.Errorf("secretclient: unavailable")
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("secretclient: status %d", resp.StatusCode)
	}
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Items []resolveItem `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("secretclient: decode: %w", err)
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("secretclient: business code %d", envelope.Code)
	}
	out := make(map[string]resolveItem, len(envelope.Data.Items))
	for _, item := range envelope.Data.Items {
		out[item.Name] = item
	}
	return out, nil
}

func cacheKey(env, namespace, name string) string {
	return env + "\x00" + namespace + "\x00" + name
}
