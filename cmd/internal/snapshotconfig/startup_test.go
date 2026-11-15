package snapshotconfig

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/configclient"
)

// hashValueForTest 与快照契约一致：SHA-256 十六进制。
func hashValueForTest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// writeSignedSnapshot 生成一份签名快照文件并返回公钥配置串。
func writeSignedSnapshot(t *testing.T, path string, values map[string]string) string {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := configclient.NewSigner("k1", privateKey)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	items := make([]configclient.Item, 0, len(values))
	for key, value := range values {
		items = append(items, configclient.Item{
			ConfigKey: key, ItemID: int64(len(items) + 1), ItemVersion: 1,
			ValueHash: hashValueForTest(value),
		})
	}
	snapshot := configclient.Snapshot{
		Env: "dev", Namespace: "platform", ReleaseID: 1, ReleaseNo: 1,
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
		t.Fatalf("new file store: %v", err)
	}
	if err := store.Save(context.Background(), snapshot); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	return "k1=" + base64.StdEncoding.EncodeToString(publicKey)
}

func TestStartupValuesWithoutFile(t *testing.T) {
	t.Setenv(FileEnv, "")
	t.Setenv(FilesEnv, "")
	values, err := StartupValues(context.Background())
	if err != nil || values != nil {
		t.Fatalf("startup values = %v, %v; want nil, nil", values, err)
	}
}

// testKeypair 生成一份签名器与公钥配置串。
func testKeypair(t *testing.T) (signer *configclient.Signer, publicKeysEnv string) {
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

// writeSnapshotFor 生成指定 namespace 的签名快照文件。
func writeSnapshotFor(t *testing.T, path, namespace string, values map[string]string,
	signer *configclient.Signer) {
	t.Helper()
	items := make([]configclient.Item, 0, len(values))
	for key, value := range values {
		items = append(items, configclient.Item{
			ConfigKey: key, ItemID: int64(len(items) + 1), ItemVersion: 1,
			ValueHash: hashValueForTest(value),
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
		t.Fatalf("new file store: %v", err)
	}
	if err := store.Save(context.Background(), snapshot); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
}

// TestLoadMultiNamespaceWithSwitch 验证多文件加载与命名空间开关。
func TestLoadMultiNamespaceWithSwitch(t *testing.T) {
	dir := t.TempDir()
	signer, publicKeys := testKeypair(t)
	platformPath := filepath.Join(dir, "platform.json")
	identityPath := filepath.Join(dir, "identity.json")
	writeSnapshotFor(t, platformPath, "platform", map[string]string{"addr": ":9001"}, signer)
	writeSnapshotFor(t, identityPath, "identity", map[string]string{"identity.enabled": "true"}, signer)

	t.Setenv(FileEnv, "")
	t.Setenv(FilesEnv, platformPath+","+identityPath)
	t.Setenv(PublicKeysEnv, publicKeys)
	t.Setenv(NamespacesEnv, "platform,identity")

	startup, err := Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if startup.Values["addr"] != ":9001" || startup.Values["identity.enabled"] != "true" {
		t.Fatalf("values = %v", startup.Values)
	}
	if len(startup.ByNamespace) != 2 || len(startup.Namespaces) != 2 {
		t.Fatalf("startup = %+v", startup)
	}
}

// TestLoadSwitchFiltersDisabledNamespace 验证未启用的 namespace 不进入取值。
func TestLoadSwitchFiltersDisabledNamespace(t *testing.T) {
	dir := t.TempDir()
	signer, publicKeys := testKeypair(t)
	platformPath := filepath.Join(dir, "platform.json")
	identityPath := filepath.Join(dir, "identity.json")
	writeSnapshotFor(t, platformPath, "platform", map[string]string{"addr": ":9001"}, signer)
	writeSnapshotFor(t, identityPath, "identity", map[string]string{"identity.enabled": "true"}, signer)

	t.Setenv(FileEnv, "")
	t.Setenv(FilesEnv, platformPath+","+identityPath)
	t.Setenv(PublicKeysEnv, publicKeys)
	t.Setenv(NamespacesEnv, "platform")

	startup, err := Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := startup.Values["identity.enabled"]; ok {
		t.Fatalf("disabled namespace leaked into values: %v", startup.Values)
	}
	if len(startup.ByNamespace) != 1 {
		t.Fatalf("by namespace = %v, want only platform", startup.ByNamespace)
	}
}

// TestLoadAllNamespacesSwitch 验证 all 开关展开为已加载的全部 namespace。
func TestLoadAllNamespacesSwitch(t *testing.T) {
	dir := t.TempDir()
	signer, publicKeys := testKeypair(t)
	platformPath := filepath.Join(dir, "platform.json")
	auditPath := filepath.Join(dir, "audit.json")
	writeSnapshotFor(t, platformPath, "platform", map[string]string{"addr": ":9001"}, signer)
	writeSnapshotFor(t, auditPath, "audit", map[string]string{"audit.retention_days": "365"}, signer)

	t.Setenv(FileEnv, "")
	t.Setenv(FilesEnv, platformPath+","+auditPath)
	t.Setenv(PublicKeysEnv, publicKeys)
	t.Setenv(NamespacesEnv, "all")

	startup, err := Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if startup.Values["audit.retention_days"] != "365" {
		t.Fatalf("values = %v", startup.Values)
	}
}

func TestStartupValuesLoadsVerifiedSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	publicKeys := writeSignedSnapshot(t, path, map[string]string{"addr": ":9000", "log.level": "debug"})
	t.Setenv(FileEnv, path)
	t.Setenv(FilesEnv, "")
	t.Setenv(PublicKeysEnv, publicKeys)

	values, err := StartupValues(context.Background())
	if err != nil {
		t.Fatalf("startup values: %v", err)
	}
	if values["addr"] != ":9000" || values["log.level"] != "debug" {
		t.Fatalf("startup values = %v", values)
	}
}

func TestStartupValuesRequiresPublicKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	writeSignedSnapshot(t, path, map[string]string{"addr": ":9000"})
	t.Setenv(FileEnv, path)
	t.Setenv(FilesEnv, "")
	t.Setenv(PublicKeysEnv, "")

	if _, err := StartupValues(context.Background()); err == nil {
		t.Fatal("missing public keys must fail")
	}
}

// TestStartupValuesRejectsTamperedSnapshot 确保签名快照被改写后不会作为可信取值。
func TestStartupValuesRejectsTamperedSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	publicKeys := writeSignedSnapshot(t, path, map[string]string{"addr": ":9000"})
	store, err := configclient.NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	snapshot, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	snapshot.Values["addr"] = ":6666"
	if err := store.Save(context.Background(), snapshot); err != nil {
		t.Fatalf("save tampered snapshot: %v", err)
	}
	t.Setenv(FileEnv, path)
	t.Setenv(FilesEnv, "")
	t.Setenv(PublicKeysEnv, publicKeys)

	if _, err := StartupValues(context.Background()); err == nil {
		t.Fatal("tampered snapshot must be rejected")
	}
}
