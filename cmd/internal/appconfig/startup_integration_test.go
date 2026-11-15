package appconfig_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/admin-frame/admin-frame/cmd/internal/appconfig"
	"github.com/admin-frame/admin-frame/cmd/internal/snapshotconfig"
	"github.com/admin-frame/admin-frame/pkg/configclient"
)

// TestSnapshotSwitchOverridesConfig 验证启用 namespace 的签名快照覆盖静态配置。
func TestSnapshotSwitchOverridesConfig(t *testing.T) {
	configPath := writeConfig(t, "admin.yaml", `
service: admin-api
channel: admin
addr: ":8080"
identity:
  lockout:
    max_failures: 5
`)
	dir := t.TempDir()
	signer, publicKeys := snapshotSigner(t)
	platformPath := filepath.Join(dir, "platform.json")
	identityPath := filepath.Join(dir, "identity.json")
	writeNamespaceSnapshot(t, signer, platformPath, "platform", map[string]string{"addr": ":9090"})
	writeNamespaceSnapshot(t, signer, identityPath, "identity",
		map[string]string{"identity.lockout.max_failures": "99"})
	t.Setenv(snapshotconfig.FileEnv, "")
	t.Setenv(snapshotconfig.FilesEnv, platformPath+","+identityPath)
	t.Setenv(snapshotconfig.PublicKeysEnv, publicKeys)
	t.Setenv(snapshotconfig.NamespacesEnv, "platform,identity")

	startup, err := snapshotconfig.Load(context.Background())
	if err != nil {
		t.Fatalf("load startup: %v", err)
	}
	cfg, err := appconfig.LoadWithValuesAllowMissing(configPath, func() (map[string]string, error) {
		return startup.Values, nil
	}, false)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Addr != ":9090" {
		t.Fatalf("addr = %q, want snapshot value", cfg.Addr)
	}
	if cfg.Identity.Lockout.MaxFailures != 99 {
		t.Fatalf("identity max failures = %d, want 99", cfg.Identity.Lockout.MaxFailures)
	}
}

// TestSnapshotSwitchDisabledNamespaceKeepsFile 验证未启用 namespace 不覆盖文件取值。
func TestSnapshotSwitchDisabledNamespaceKeepsFile(t *testing.T) {
	configPath := writeConfig(t, "admin.yaml", `
service: admin-api
channel: admin
addr: ":8080"
identity:
  lockout:
    max_failures: 5
`)
	dir := t.TempDir()
	signer, publicKeys := snapshotSigner(t)
	platformPath := filepath.Join(dir, "platform.json")
	identityPath := filepath.Join(dir, "identity.json")
	writeNamespaceSnapshot(t, signer, platformPath, "platform", map[string]string{"addr": ":9090"})
	writeNamespaceSnapshot(t, signer, identityPath, "identity",
		map[string]string{"identity.lockout.max_failures": "99"})
	t.Setenv(snapshotconfig.FileEnv, "")
	t.Setenv(snapshotconfig.FilesEnv, platformPath+","+identityPath)
	t.Setenv(snapshotconfig.PublicKeysEnv, publicKeys)
	t.Setenv(snapshotconfig.NamespacesEnv, "platform")

	startup, err := snapshotconfig.Load(context.Background())
	if err != nil {
		t.Fatalf("load startup: %v", err)
	}
	cfg, err := appconfig.LoadWithValuesAllowMissing(configPath, func() (map[string]string, error) {
		return startup.Values, nil
	}, false)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Addr != ":9090" {
		t.Fatalf("addr = %q, want snapshot value", cfg.Addr)
	}
	if cfg.Identity.Lockout.MaxFailures != 5 {
		t.Fatalf("disabled namespace leaked: max failures = %d, want file value 5",
			cfg.Identity.Lockout.MaxFailures)
	}
}

// TestLoadWithoutConfigFile 验证无文件启动：部署身份走环境变量，其余取快照。
func TestLoadWithoutConfigFile(t *testing.T) {
	t.Setenv("ADMIN_FRAME_SERVICE", "admin-api")
	t.Setenv("ADMIN_FRAME_CHANNEL", "admin")
	t.Setenv("ADMIN_FRAME_ENV", "dev")
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	values := map[string]string{"server.addr": ":9090"}

	cfg, err := appconfig.LoadWithValuesAllowMissing(missing, func() (map[string]string, error) {
		return values, nil
	}, true)
	if err != nil {
		t.Fatalf("load without file: %v", err)
	}
	if cfg.Service != "admin-api" || cfg.Channel != "admin" {
		t.Fatalf("deployment identity = %s/%s", cfg.Service, cfg.Channel)
	}
	if cfg.Addr != ":9090" {
		t.Fatalf("addr = %q, want snapshot value", cfg.Addr)
	}
}

// snapshotSigner 生成签名器与公钥配置串。
func snapshotSigner(t *testing.T) (signer *configclient.Signer, publicKeysEnv string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err = configclient.NewSigner("k1", privateKey)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	publicKeysEnv = "k1=" + base64.StdEncoding.EncodeToString(publicKey)
	return signer, publicKeysEnv
}

// writeNamespaceSnapshot 写入指定 namespace 的签名快照文件。
func writeNamespaceSnapshot(t *testing.T, signer *configclient.Signer, path, namespace string,
	values map[string]string) {
	t.Helper()
	items := make([]configclient.Item, 0, len(values))
	for key, value := range values {
		sum := sha256.Sum256([]byte(value))
		items = append(items, configclient.Item{
			ConfigKey: key, ItemID: int64(len(items) + 1), ItemVersion: 1,
			ValueHash: hex.EncodeToString(sum[:]),
		})
	}
	snapshot := configclient.Snapshot{
		Env: "dev", Namespace: namespace, ReleaseID: 1, ReleaseNo: 1,
		SnapshotHash: configclient.HashItems(items), SnapshotSize: len(items),
		Items: items, Values: values,
	}
	keyID, signature, err := signer.Sign(snapshot)
	if err != nil {
		t.Fatalf("sign snapshot: %v", err)
	}
	snapshot.KeyID = keyID
	snapshot.Signature = signature
	store, err := configclient.NewFileStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Save(context.Background(), snapshot); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
}
