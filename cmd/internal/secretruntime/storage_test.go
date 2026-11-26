package secretruntime_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/cmd/internal/secretruntime"
	"github.com/admin-frame/admin-frame/pkg/secretclient"
)

// TestResolveStorageS3Credentials 验证对象存储凭据只从密钥子域解析（CC-R-05）。
func TestResolveStorageS3Credentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"items": []map[string]any{
					{
						"name": "storage.s3.access_key", "version": 1,
						"value":            "rustfsadmin",
						"lease_expires_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano),
					},
					{
						"name": "storage.s3.secret_key", "version": 1,
						"value":            "rustfsadmin-secret",
						"lease_expires_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano),
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	client, err := secretclient.New(secretclient.Config{
		BaseURL: srv.URL, ServiceIdentity: "spiffe://local/test",
	})
	if err != nil {
		t.Fatal(err)
	}
	ak, sk, err := secretruntime.ResolveStorageS3Credentials(context.Background(), "dev", client)
	if err != nil {
		t.Fatalf("ResolveStorageS3Credentials: %v", err)
	}
	if ak != "rustfsadmin" || sk != "rustfsadmin-secret" {
		t.Fatalf("ak/sk = %q/%q", ak, sk)
	}
}

// TestResolveStorageS3CredentialsRequiresClient 验证缺少 client 时 fast-fail，
// 不会静默回退到环境变量明文。
func TestResolveStorageS3CredentialsRequiresClient(t *testing.T) {
	if _, _, err := secretruntime.ResolveStorageS3Credentials(context.Background(), "dev", nil); err == nil {
		t.Fatal("nil client must fail instead of falling back to env")
	}
}

// TestResolveStorageS3CredentialsRejectsEmpty 验证空值视为解析失败，
// 避免把空凭据写进配置后被存储层当成「匿名访问」。
func TestResolveStorageS3CredentialsRejectsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"items": []map[string]any{{
					"name": "storage.s3.access_key", "version": 1,
					"value":            "   ",
					"lease_expires_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano),
				}},
			},
		})
	}))
	t.Cleanup(srv.Close)

	client, err := secretclient.New(secretclient.Config{
		BaseURL: srv.URL, ServiceIdentity: "spiffe://local/test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := secretruntime.ResolveStorageS3Credentials(context.Background(), "dev", client); err == nil {
		t.Fatal("blank credential must be rejected")
	}
}
