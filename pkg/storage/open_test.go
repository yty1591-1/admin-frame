package storage_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/storage"
	_ "github.com/admin-frame/admin-frame/pkg/storage/cos"
	_ "github.com/admin-frame/admin-frame/pkg/storage/obs"
	_ "github.com/admin-frame/admin-frame/pkg/storage/oss"
	_ "github.com/admin-frame/admin-frame/pkg/storage/s3"
)

func TestOpenMemoryAndLocal(t *testing.T) {
	ctx := context.Background()
	mem, err := storage.Open(ctx, storage.Config{Driver: "memory"})
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()

	local, err := storage.Open(ctx, storage.Config{
		Driver: "local",
		Local:  storage.LocalConfig{Root: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
}

func TestOpenUnknownDriver(t *testing.T) {
	_, err := storage.Open(context.Background(), storage.Config{Driver: "nope"})
	if !errors.Is(err, storage.ErrUnknownDriver) {
		t.Fatalf("err=%v", err)
	}
}

func TestDriversRegistered(t *testing.T) {
	got := storage.Drivers()
	sort.Strings(got)
	need := []string{"cos", "local", "memory", "minio", "obs", "oss", "s3"}
	for _, n := range need {
		found := false
		for _, g := range got {
			if g == n {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing driver %q in %v", n, got)
		}
	}
}

func TestOpenS3Validation(t *testing.T) {
	_, err := storage.Open(context.Background(), storage.Config{
		Driver: "s3",
		S3:     storage.S3Config{Region: "us-east-1"},
	})
	if err == nil || !strings.Contains(err.Error(), "bucket") {
		t.Fatalf("err=%v", err)
	}

	_, err = storage.Open(context.Background(), storage.Config{
		Driver: "minio",
		S3: storage.S3Config{
			Bucket: "b",
			Region: "us-east-1",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("err=%v", err)
	}
}

func TestOpenNativeUnsupported(t *testing.T) {
	_, err := storage.Open(context.Background(), storage.Config{
		Driver: "oss",
		OSS:    storage.OSSConfig{Native: true, Bucket: "b", Region: "cn-hangzhou", Endpoint: "oss.aliyuncs.com"},
		S3:     storage.S3Config{Bucket: "b", Region: "cn-hangzhou"},
	})
	if !errors.Is(err, storage.ErrUnsupported) {
		t.Fatalf("err=%v", err)
	}
}

func TestEffectiveS3Merge(t *testing.T) {
	cfg := storage.Config{
		Driver: "oss",
		S3:     storage.S3Config{Region: "us-east-1", Bucket: "base", AccessKey: "a"},
		OSS:    storage.OSSConfig{Endpoint: "oss-cn-hangzhou.aliyuncs.com", Bucket: "oss-bucket"},
	}
	sc := cfg.EffectiveS3()
	if sc.Endpoint != "oss-cn-hangzhou.aliyuncs.com" || sc.Bucket != "oss-bucket" || sc.AccessKey != "a" {
		t.Fatalf("%+v", sc)
	}
}
