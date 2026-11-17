package configclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// HTTPSource 通过 config-api 拉取非敏感配置快照（ADR-0016）。
type HTTPSource struct {
	baseURL         string
	serviceIdentity string
	http            *http.Client

	mu        sync.Mutex
	keys      map[string]string   // env/namespace → release_key
	lastSnaps map[string]Snapshot // env/namespace → last successful
}

// HTTPSourceConfig 是 HTTPSource 装配参数。
type HTTPSourceConfig struct {
	BaseURL         string
	ServiceIdentity string
	HTTPClient      *http.Client
}

// NewHTTPSource 构造 HTTP 配置源。
func NewHTTPSource(cfg HTTPSourceConfig) (*HTTPSource, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	identity := strings.TrimSpace(cfg.ServiceIdentity)
	if base == "" || identity == "" {
		return nil, fmt.Errorf("configclient: http source requires base url and service identity")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &HTTPSource{
		baseURL: base, serviceIdentity: identity, http: client,
		keys: make(map[string]string), lastSnaps: make(map[string]Snapshot),
	}, nil
}

type pullEnvelope struct {
	Code int `json:"code"`
	Data struct {
		Env            string            `json:"env"`
		Namespace      string            `json:"namespace"`
		ReleaseID      int64             `json:"release_id"`
		ReleaseNo      int64             `json:"release_no"`
		PointerVersion int64             `json:"pointer_version"`
		ReleaseKey     string            `json:"release_key"`
		Values         map[string]string `json:"values"`
		GeneratedAt    time.Time         `json:"generated_at"`
	} `json:"data"`
	Message string `json:"message"`
}

// Load 实现 Source：带 release_key 拉取；304 时返回上一份缓存的元数据错误由调用方刷新处理。
//
// 304 时若本源无内存副本，返回错误，由 Client 走本地 DirCache。
func (s *HTTPSource) Load(ctx context.Context, env, namespace string) (Snapshot, error) {
	cacheKey := env + "/" + namespace
	s.mu.Lock()
	releaseKey := s.keys[cacheKey]
	s.mu.Unlock()

	endpoint := fmt.Sprintf("%s/internal/v1/configs/%s/%s", s.baseURL,
		url.PathEscape(env), url.PathEscape(namespace))
	if releaseKey != "" {
		endpoint += "?release_key=" + url.QueryEscape(releaseKey)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return Snapshot{}, err
	}
	req.Header.Set("X-Service-Identity", s.serviceIdentity)
	req.Header.Set("Accept", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return Snapshot{}, fmt.Errorf("configclient: pull config: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified {
		s.mu.Lock()
		prev, ok := s.lastSnaps[cacheKey]
		s.mu.Unlock()
		if ok {
			return prev, nil
		}
		return Snapshot{}, fmt.Errorf("configclient: not modified without local copy")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Snapshot{}, fmt.Errorf("configclient: read pull response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Snapshot{}, fmt.Errorf("configclient: pull status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var envelope pullEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Snapshot{}, fmt.Errorf("configclient: decode pull response: %w", err)
	}
	if envelope.Code != 0 {
		return Snapshot{}, fmt.Errorf("configclient: pull code %d: %s", envelope.Code, envelope.Message)
	}
	data := envelope.Data
	if data.Values == nil {
		data.Values = map[string]string{}
	}
	snap := Snapshot{
		Env: data.Env, Namespace: data.Namespace,
		ReleaseID: data.ReleaseID, ReleaseNo: data.ReleaseNo,
		PointerVersion: data.PointerVersion,
		Values:         data.Values,
		GeneratedAt:    data.GeneratedAt.UTC(),
		SnapshotSize:   len(data.Values),
	}
	s.mu.Lock()
	s.keys[cacheKey] = data.ReleaseKey
	s.lastSnaps[cacheKey] = snap
	s.mu.Unlock()
	return snap, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
