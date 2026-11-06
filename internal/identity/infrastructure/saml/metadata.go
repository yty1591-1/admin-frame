// Package saml 加载 IdP 元数据并做有限刷新。
package saml

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	crewjam "github.com/crewjam/saml"

	"github.com/admin-frame/admin-frame/pkg/httpclient"
)

// maxMetadataBytes 限制元数据体积，避免超大响应拖垮进程。
const maxMetadataBytes = 1 << 20

// MetadataConfig 是 IdP 元数据来源配置。
type MetadataConfig struct {
	// URL 与 XML 二选一；XML 为内联元数据（便于离线部署）。
	URL string
	XML string
	// EntityID 非空时校验元数据实体与之一致。
	EntityID string
	// Timeout 单次拉取超时。
	Timeout time.Duration
	// TTL 元数据缓存有效期；0 表示只在启动时加载。
	TTL time.Duration
	// MinRefresh 两次拉取的最小间隔，避免故障期打爆 IdP。
	MinRefresh time.Duration
}

// MetadataSource 缓存 IdP 元数据；刷新失败时保留上一份可用值。
type MetadataSource struct {
	cfg    MetadataConfig
	client *http.Client

	mu          sync.RWMutex
	metadata    *crewjam.EntityDescriptor
	fetchedAt   time.Time
	lastAttempt time.Time
}

// NewMetadataSource 在启动期加载一次元数据；失败即启动失败（失败关闭）。
func NewMetadataSource(ctx context.Context, cfg MetadataConfig, client *http.Client) (*MetadataSource, error) {
	if strings.TrimSpace(cfg.URL) == "" && strings.TrimSpace(cfg.XML) == "" {
		return nil, errors.New("saml: metadata_url or metadata_xml is required")
	}
	if strings.TrimSpace(cfg.URL) != "" && strings.TrimSpace(cfg.XML) != "" {
		return nil, errors.New("saml: metadata_url and metadata_xml are mutually exclusive")
	}
	if client == nil {
		built, err := httpclient.New(httpclient.Config{Timeout: cfg.Timeout})
		if err != nil {
			return nil, err
		}
		client = built.HTTP()
	}
	source := &MetadataSource{cfg: cfg, client: client}
	if _, err := source.load(ctx); err != nil {
		return nil, err
	}
	return source, nil
}

// Get 返回可用元数据；超过 TTL 时尝试刷新，失败则沿用旧值。
func (m *MetadataSource) Get(ctx context.Context) (*crewjam.EntityDescriptor, error) {
	m.mu.RLock()
	cached := m.metadata
	fetchedAt := m.fetchedAt
	m.mu.RUnlock()
	if cached != nil && (m.cfg.TTL <= 0 || time.Since(fetchedAt) < m.cfg.TTL) {
		return cached, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.metadata != nil && (m.cfg.TTL <= 0 || time.Since(m.fetchedAt) < m.cfg.TTL) {
		return m.metadata, nil
	}
	if m.metadata != nil && m.cfg.MinRefresh > 0 && time.Since(m.lastAttempt) < m.cfg.MinRefresh {
		return m.metadata, nil
	}
	loaded, err := m.loadLocked(ctx)
	if err != nil {
		if m.metadata != nil {
			// 刷新失败保留旧元数据：签名校验仍可继续，未知证书会失败关闭。
			return m.metadata, nil
		}
		return nil, err
	}
	return loaded, nil
}

// load 无锁加载，仅启动期使用。
func (m *MetadataSource) load(ctx context.Context) (*crewjam.EntityDescriptor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadLocked(ctx)
}

// loadLocked 调用方必须持有写锁。
func (m *MetadataSource) loadLocked(ctx context.Context) (*crewjam.EntityDescriptor, error) {
	m.lastAttempt = time.Now()
	raw, err := m.fetch(ctx)
	if err != nil {
		return nil, err
	}
	var md crewjam.EntityDescriptor
	if err := xml.Unmarshal(raw, &md); err != nil {
		return nil, fmt.Errorf("saml: parse idp metadata: %w", err)
	}
	if expected := strings.TrimSpace(m.cfg.EntityID); expected != "" && md.EntityID != expected {
		return nil, fmt.Errorf("saml: idp metadata entity_id mismatch: got %q want %q", md.EntityID, expected)
	}
	m.metadata = &md
	m.fetchedAt = time.Now()
	return m.metadata, nil
}

// fetch 返回元数据原始 XML；内联模式直接返回配置值。
func (m *MetadataSource) fetch(ctx context.Context) ([]byte, error) {
	if inline := strings.TrimSpace(m.cfg.XML); inline != "" {
		return []byte(inline), nil
	}
	reqCtx := ctx
	var cancel context.CancelFunc
	if m.cfg.Timeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, m.cfg.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimSpace(m.cfg.URL), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("saml: build metadata request: %w", err)
	}
	req.Header.Set("Accept", "application/xml, text/xml")
	response, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("saml: fetch idp metadata: %w", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("saml: fetch idp metadata: unexpected status %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, maxMetadataBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("saml: read idp metadata: %w", err)
	}
	if len(raw) > maxMetadataBytes {
		return nil, errors.New("saml: idp metadata is too large")
	}
	return raw, nil
}
