package saml

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestMetadataSourceLoadsInlineXML 校验内联元数据加载与 entityID 校验。
func TestMetadataSourceLoadsInlineXML(t *testing.T) {
	t.Parallel()
	source, err := NewMetadataSource(context.Background(), MetadataConfig{
		XML:      testIDPMetadata(t, "https://idp.example.com/metadata"),
		EntityID: "https://idp.example.com/metadata",
	}, nil)
	if err != nil {
		t.Fatalf("new metadata source: %v", err)
	}
	descriptor, err := source.Get(context.Background())
	if err != nil {
		t.Fatalf("get metadata: %v", err)
	}
	if descriptor.EntityID != "https://idp.example.com/metadata" {
		t.Fatalf("unexpected entity id: %q", descriptor.EntityID)
	}
}

// TestMetadataSourceRejectsBothSources 校验 URL 与 XML 同时配置时失败。
func TestMetadataSourceRejectsBothSources(t *testing.T) {
	t.Parallel()
	_, err := NewMetadataSource(context.Background(), MetadataConfig{
		URL: "https://idp.example.com/metadata",
		XML: testIDPMetadata(t, "https://idp.example.com/metadata"),
	}, nil)
	if err == nil {
		t.Fatalf("expected mutually exclusive error")
	}
}

// TestMetadataSourceRetainsCacheOnRefreshFailure 校验刷新失败保留旧元数据。
func TestMetadataSourceRetainsCacheOnRefreshFailure(t *testing.T) {
	t.Parallel()
	var failing atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(testIDPMetadata(t, "https://idp.example.com/metadata")))
	}))
	defer server.Close()

	source, err := NewMetadataSource(context.Background(), MetadataConfig{
		URL:        server.URL,
		EntityID:   "https://idp.example.com/metadata",
		Timeout:    time.Second,
		TTL:        20 * time.Millisecond,
		MinRefresh: time.Millisecond,
	}, server.Client())
	if err != nil {
		t.Fatalf("new metadata source: %v", err)
	}
	if _, err := source.Get(context.Background()); err != nil {
		t.Fatalf("initial get: %v", err)
	}
	failing.Store(true)
	time.Sleep(40 * time.Millisecond)
	descriptor, err := source.Get(context.Background())
	if err != nil {
		t.Fatalf("refresh failure should retain cache, got %v", err)
	}
	if !strings.Contains(descriptor.EntityID, "idp.example.com") {
		t.Fatalf("unexpected cached metadata: %+v", descriptor.EntityID)
	}
}
