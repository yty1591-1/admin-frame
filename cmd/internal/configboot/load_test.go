package configboot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/configclient"
)

func TestLoadRequiresEveryNamespaceFromRemoteOrCache(t *testing.T) {
	for _, cached := range []bool{false, true} {
		name := "missing"
		if cached {
			name = "cached"
		}
		t.Run(name, func(t *testing.T) {
			cache, err := configclient.NewDirCache(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if cached {
				err = cache.SaveNamespace(context.Background(), configclient.Snapshot{Env: "dev", Namespace: "identity", Values: map[string]string{"identity.enabled": "true"}})
				if err != nil {
					t.Fatal(err)
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/internal/v1/install/status":
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"env": "dev", "status": "ready"}})
				case "/internal/v1/configs/dev/platform":
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"env": "dev", "namespace": "platform", "values": map[string]string{"log.level": "info"}}})
				default:
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			}))
			defer server.Close()
			t.Setenv("CONFIG_API_URL", server.URL)
			t.Setenv("CONFIG_SERVICE_IDENTITY", "admin-api")
			t.Setenv("CONFIGCENTER_ENV", "dev")
			t.Setenv("CONFIGCENTER_NAMESPACES", "platform,identity")
			t.Setenv("CONFIGCENTER_CACHE_DIR", cache.Dir)
			t.Setenv("CONFIG_API_CLIENT_CERT", "")
			t.Setenv("CONFIG_API_CLIENT_KEY", "")
			t.Setenv("CONFIG_API_TLS_CA", "")
			result, err := Load(context.Background())
			if !cached {
				if err == nil || !strings.Contains(err.Error(), "identity") {
					t.Fatalf("missing namespace accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Values["identity.enabled"] != "true" || result.Values["log.level"] != "info" {
				t.Fatalf("incomplete values: %v", result.Values)
			}
		})
	}
}
