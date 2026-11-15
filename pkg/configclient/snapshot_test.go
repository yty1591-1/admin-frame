package configclient

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"sort"
	"testing"
)

// testKey 生成一对确定可用的 Ed25519 密钥。
func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	return publicKey, privateKey
}

// buildSignedSnapshot 按取值集合构造一份签名快照，返回快照与匹配的验签器。
func buildSignedSnapshot(t *testing.T, values map[string]string) (Snapshot, *Verifier) {
	t.Helper()
	publicKey, privateKey := testKey(t)
	signer, err := NewSigner("k1", privateKey)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	verifier, err := NewVerifier(map[string]ed25519.PublicKey{"k1": publicKey})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]Item, 0, len(keys))
	for index, key := range keys {
		items = append(items, Item{
			ConfigKey: key, ItemID: int64(10 + index), ItemVersion: 1,
			ValueHash: hashValue(values[key]),
		})
	}
	snapshot := Snapshot{
		Env: "dev", Namespace: "platform", ReleaseID: 9001, ReleaseNo: 3,
		SnapshotHash: HashItems(items), SnapshotSize: len(items),
		Items: items, Values: values,
	}
	keyID, signature, err := signer.Sign(snapshot)
	if err != nil {
		t.Fatalf("sign snapshot: %v", err)
	}
	snapshot.KeyID = keyID
	snapshot.Signature = signature
	return snapshot, verifier
}

// signedSnapshot 是默认取值集合的签名快照。
func signedSnapshot(t *testing.T) (Snapshot, *Verifier) {
	t.Helper()
	return buildSignedSnapshot(t, map[string]string{"http.timeout": "10s", "log.level": "info"})
}

func TestVerifierAcceptsSignedSnapshot(t *testing.T) {
	snapshot, verifier := signedSnapshot(t)
	if err := verifier.Verify(snapshot); err != nil {
		t.Fatalf("verify signed snapshot: %v", err)
	}
}

func TestVerifierRejectsUnsignedSnapshot(t *testing.T) {
	snapshot, verifier := signedSnapshot(t)
	snapshot.Signature = ""
	if err := verifier.Verify(snapshot); !errors.Is(err, ErrSnapshotUnsigned) {
		t.Fatalf("verify unsigned = %v, want ErrSnapshotUnsigned", err)
	}
}

func TestVerifierRejectsUnknownKeyID(t *testing.T) {
	snapshot, verifier := signedSnapshot(t)
	snapshot.KeyID = "k-unknown"
	if err := verifier.Verify(snapshot); !errors.Is(err, ErrSnapshotUnknownKeyID) {
		t.Fatalf("verify unknown kid = %v, want ErrSnapshotUnknownKeyID", err)
	}
}

func TestVerifierRejectsTamperedValues(t *testing.T) {
	snapshot, verifier := signedSnapshot(t)
	snapshot.Values["log.level"] = "debug"
	if err := verifier.Verify(snapshot); !errors.Is(err, ErrSnapshotValueMismatch) {
		t.Fatalf("verify tampered value = %v, want ErrSnapshotValueMismatch", err)
	}
}

func TestVerifierRejectsTamperedHashOrSize(t *testing.T) {
	snapshot, verifier := signedSnapshot(t)
	snapshot.SnapshotHash = hashValue("other")
	if err := verifier.Verify(snapshot); !errors.Is(err, ErrSnapshotHashMismatch) {
		t.Fatalf("verify tampered hash = %v, want ErrSnapshotHashMismatch", err)
	}

	snapshot, verifier = signedSnapshot(t)
	snapshot.SnapshotSize++
	if err := verifier.Verify(snapshot); !errors.Is(err, ErrSnapshotSizeMismatch) {
		t.Fatalf("verify tampered size = %v, want ErrSnapshotSizeMismatch", err)
	}
}

func TestVerifierRejectsTamperedSignature(t *testing.T) {
	snapshot, verifier := signedSnapshot(t)
	raw, err := base64.StdEncoding.DecodeString(snapshot.Signature)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	raw[0] ^= 0xff
	snapshot.Signature = base64.StdEncoding.EncodeToString(raw)
	if err := verifier.Verify(snapshot); !errors.Is(err, ErrSnapshotSignatureInvalid) {
		t.Fatalf("verify tampered signature = %v, want ErrSnapshotSignatureInvalid", err)
	}
}

// TestVerifierDualKeyRotationWindow 覆盖 M3 签名钥轮换：双钥验签窗口内新旧 key_id 均可通过。
func TestVerifierDualKeyRotationWindow(t *testing.T) {
	oldPub, oldPriv := testKey(t)
	newPub, newPriv := testKey(t)
	verifier, err := NewVerifier(map[string]ed25519.PublicKey{
		"k-old": oldPub,
		"k-new": newPub,
	})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"http.timeout": "5s"}
	signWith := func(kid string, priv ed25519.PrivateKey) Snapshot {
		t.Helper()
		signer, serr := NewSigner(kid, priv)
		if serr != nil {
			t.Fatal(serr)
		}
		snap, _ := buildSignedSnapshot(t, values)
		snap.KeyID = ""
		snap.Signature = ""
		id, sig, serr := signer.Sign(snap)
		if serr != nil {
			t.Fatal(serr)
		}
		snap.KeyID, snap.Signature = id, sig
		return snap
	}
	if err := verifier.Verify(signWith("k-old", oldPriv)); err != nil {
		t.Fatalf("old key window: %v", err)
	}
	if err := verifier.Verify(signWith("k-new", newPriv)); err != nil {
		t.Fatalf("new key window: %v", err)
	}
	// 吊销旧钥后仅保留新钥。
	revoked, err := NewVerifier(map[string]ed25519.PublicKey{"k-new": newPub})
	if err != nil {
		t.Fatal(err)
	}
	if err := revoked.Verify(signWith("k-old", oldPriv)); !errors.Is(err, ErrSnapshotUnknownKeyID) {
		t.Fatalf("revoked old key = %v, want ErrSnapshotUnknownKeyID", err)
	}
}

func TestHashItemsIsOrderIndependent(t *testing.T) {
	first := []Item{
		{ConfigKey: "b.key", ItemID: 2, ItemVersion: 1, ValueHash: hashValue("b")},
		{ConfigKey: "a.key", ItemID: 1, ItemVersion: 1, ValueHash: hashValue("a")},
	}
	second := []Item{first[1], first[0]}
	if HashItems(first) != HashItems(second) {
		t.Fatal("snapshot hash must not depend on item order")
	}
}

func TestParsePublicKeys(t *testing.T) {
	publicKey, _ := testKey(t)
	raw := "k1=" + base64.StdEncoding.EncodeToString(publicKey)
	keys, err := ParsePublicKeys(raw)
	if err != nil {
		t.Fatalf("parse public keys: %v", err)
	}
	if len(keys) != 1 || len(keys["k1"]) != ed25519.PublicKeySize {
		t.Fatalf("parsed keys mismatch: %v", keys)
	}
	if _, err := ParsePublicKeys("k1=not-base64"); err == nil {
		t.Fatal("invalid base64 must be rejected")
	}
	if _, err := ParsePublicKeys(""); err == nil {
		t.Fatal("empty key set must be rejected")
	}
}
