package configboot

import (
	"strings"
	"testing"

	appconfig "github.com/admin-frame/admin-frame/cmd/internal/appconfig"
)

func TestApplyConfigAPIBootstrapValuesProd(t *testing.T) {
	t.Setenv("ADMIN_FRAME_ID_NODE_ID", "1")
	t.Setenv("ADMIN_FRAME_DB_DRIVER", "mysql")
	t.Setenv("ADMIN_FRAME_DB_DSN", "root:root@tcp(127.0.0.1:3306)/admin_frame_test?parseTime=true&loc=UTC&charset=utf8mb4")

	cfg, err := appconfig.LoadFromValues(func() (map[string]string, error) {
		values := map[string]string{
			"service": "config-api",
			"channel": "internal",
			"addr":    ":8085",
			"env":     "prod",
		}
		ApplyConfigAPIBootstrapValues(values, "prod")
		return values, nil
	})
	if err != nil {
		t.Fatalf("LoadFromValues prod bootstrap: %v", err)
	}
	if cfg.Auth.Mode != "jwt" || cfg.Storage.Driver != "local" {
		t.Fatalf("auth/storage = mode=%q driver=%q", cfg.Auth.Mode, cfg.Storage.Driver)
	}
	if cfg.ID.NodeID == nil || *cfg.ID.NodeID != 1 {
		t.Fatalf("node_id = %v, want 1", cfg.ID.NodeID)
	}
}

func TestApplyConfigAPIBootstrapValuesProdMissingNodeID(t *testing.T) {
	t.Setenv("ADMIN_FRAME_ID_NODE_ID", "")
	t.Setenv("ADMIN_FRAME_DB_DRIVER", "mysql")
	t.Setenv("ADMIN_FRAME_DB_DSN", "root:root@tcp(127.0.0.1:3306)/admin_frame_test?parseTime=true&loc=UTC&charset=utf8mb4")

	_, err := appconfig.LoadFromValues(func() (map[string]string, error) {
		values := map[string]string{
			"service": "config-api",
			"channel": "internal",
			"addr":    ":8085",
			"env":     "prod",
		}
		ApplyConfigAPIBootstrapValues(values, "prod")
		return values, nil
	})
	if err == nil || !strings.Contains(err.Error(), "id.node_id is required") {
		t.Fatalf("err=%v want id.node_id required", err)
	}
}

func TestApplyDeployNodeIDAndStorage(t *testing.T) {
	t.Setenv("ADMIN_FRAME_ID_NODE_ID", "7")
	t.Setenv("ADMIN_FRAME_STORAGE_S3_ENDPOINT", "http://127.0.0.1:9000")
	t.Setenv("ADMIN_FRAME_STORAGE_S3_BUCKET", "admin-frame")
	t.Setenv("ADMIN_FRAME_STORAGE_S3_REGION", "us-east-1")
	t.Setenv("ADMIN_FRAME_STORAGE_S3_ACCESS_KEY", "minioadmin")
	t.Setenv("ADMIN_FRAME_STORAGE_S3_SECRET_KEY", "minioadmin")
	t.Setenv("ADMIN_FRAME_STORAGE_S3_FORCE_PATH_STYLE", "true")

	values := map[string]string{"storage.driver": "s3"}
	ApplyDeployOverlays(values)
	if values["id.node_id"] != "7" {
		t.Fatalf("node_id = %q", values["id.node_id"])
	}
	if values["storage.s3.endpoint"] != "http://127.0.0.1:9000" {
		t.Fatalf("endpoint = %q", values["storage.s3.endpoint"])
	}
	if values["storage.s3.force_path_style"] != "true" {
		t.Fatalf("force_path_style = %q", values["storage.s3.force_path_style"])
	}

	memory := map[string]string{"storage.driver": "memory"}
	ApplyDeployStorage(memory)
	if memory["storage.s3.endpoint"] != "" {
		t.Fatalf("memory driver must skip s3 inject: %+v", memory)
	}
}

