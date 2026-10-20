package platform_test

import (
	"context"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/pkg/storage"
)

func TestOpenStorageSetNamedBuckets(t *testing.T) {
	set, err := platform.OpenStorageSet(context.Background(), platform.StorageConfig{
		Driver:    "memory",
		KeyPrefix: "default/",
		Buckets: map[string]platform.BucketConfig{
			"public": {
				Driver:    "memory",
				KeyPrefix: "public/",
			},
			"exports": {
				Driver: "memory",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// 忽略：测试清理阶段关闭存储集合，关闭失败不影响断言。
		_ = set.Close()
	})

	def, ok := set.Get("default")
	if !ok || def == nil {
		t.Fatal("default missing")
	}
	pub, ok := set.Get("public")
	if !ok {
		t.Fatal("public missing")
	}
	names := set.Names()
	if len(names) != 2 || names[0] != "exports" || names[1] != "public" {
		t.Fatalf("names=%v", names)
	}
	if err := set.ReadyAll(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, err = def.Put(context.Background(), "a.txt", strings.NewReader("d"), storage.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pub.Put(context.Background(), "a.txt", strings.NewReader("p"), storage.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// 前缀隔离：同逻辑键互不影响
	okExist, err := def.Exists(context.Background(), "a.txt")
	if err != nil || !okExist {
		t.Fatalf("default exists=%v err=%v", okExist, err)
	}
}

func TestOpenStorageSetRejectsDefaultName(t *testing.T) {
	_, err := platform.OpenStorageSet(context.Background(), platform.StorageConfig{
		Driver: "memory",
		Buckets: map[string]platform.BucketConfig{
			"default": {Driver: "memory"},
		},
	})
	if err == nil {
		t.Fatal("expected reserved name error")
	}
}

func TestValidateStorageNamedBucket(t *testing.T) {
	err := platform.ValidateStorage("prod", platform.StorageConfig{
		Driver: "s3",
		S3:     storage.S3Config{Bucket: "a", Region: "us-east-1"},
		Buckets: map[string]platform.BucketConfig{
			"public": {Driver: "memory"},
		},
	})
	if err == nil {
		t.Fatal("expected named memory forbidden in prod")
	}
}
