package configcenterruntime

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
