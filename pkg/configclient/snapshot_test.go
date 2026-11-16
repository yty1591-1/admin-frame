package configclient

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

func buildSnapshot(t *testing.T, values map[string]string) Snapshot {
	t.Helper()
	items := make([]Item, 0, len(values))
	i := int64(1)
	for key, value := range values {
		sum := sha256.Sum256([]byte(value))
		items = append(items, Item{
			ConfigKey: key, ItemID: i, ItemVersion: 1,
			ValueHash: hex.EncodeToString(sum[:]),
		})
		i++
	}
	return Snapshot{
		Env: "dev", Namespace: "platform", ReleaseID: 1, ReleaseNo: 1,
		SnapshotHash: HashItems(items), SnapshotSize: len(items),
		GeneratedAt: time.Unix(1_700_000_000, 0).UTC(),
		Items: items, Values: values,
	}
}

func TestValidateIntegrityAccepts(t *testing.T) {
	snapshot := buildSnapshot(t, map[string]string{"http.read_timeout": "5s"})
	if err := snapshot.ValidateIntegrity(); err != nil {
		t.Fatalf("ValidateIntegrity = %v", err)
	}
}

func TestValidateIntegrityRejectsHashMismatch(t *testing.T) {
	snapshot := buildSnapshot(t, map[string]string{"http.read_timeout": "5s"})
	snapshot.SnapshotHash = "deadbeef"
	if err := snapshot.ValidateIntegrity(); !errors.Is(err, ErrSnapshotHashMismatch) {
		t.Fatalf("err = %v, want ErrSnapshotHashMismatch", err)
	}
}

func TestValidateIntegrityRejectsSizeMismatch(t *testing.T) {
	snapshot := buildSnapshot(t, map[string]string{"http.read_timeout": "5s"})
	snapshot.SnapshotSize = 99
	if err := snapshot.ValidateIntegrity(); !errors.Is(err, ErrSnapshotSizeMismatch) {
		t.Fatalf("err = %v, want ErrSnapshotSizeMismatch", err)
	}
}

func TestValidateIntegrityRejectsValueMismatch(t *testing.T) {
	snapshot := buildSnapshot(t, map[string]string{"http.read_timeout": "5s"})
	snapshot.Values["http.read_timeout"] = "tampered"
	if err := snapshot.ValidateIntegrity(); !errors.Is(err, ErrSnapshotValueMismatch) {
		t.Fatalf("err = %v, want ErrSnapshotValueMismatch", err)
	}
}
