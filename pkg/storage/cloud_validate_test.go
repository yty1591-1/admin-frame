package storage_test

import (
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

func TestValidateCloudL2(t *testing.T) {
	err := storage.ValidateCloudL2("oss", storage.Config{
		OSS: storage.OSSConfig{Native: true, Bucket: "b", Region: "cn-hangzhou", AccessKey: "a", SecretKey: "s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = storage.ValidateCloudL2("obs", storage.Config{
		OBS: storage.OBSConfig{Bucket: "b", AccessKey: "a", SecretKey: "s"},
	})
	if err == nil || !errors.Is(err, storage.ErrInvalidConfig) {
		t.Fatalf("expected endpoint required, got %v", err)
	}
}

func TestNativeParamsFallback(t *testing.T) {
	p := (storage.Config{
		S3:  storage.S3Config{AccessKey: "s3ak", SecretKey: "s3sk", Bucket: "s3b"},
		OSS: storage.OSSConfig{Bucket: "ossb", Region: "cn-hangzhou"},
	}).NativeParams("oss")
	if p.Bucket != "ossb" || p.AccessKey != "s3ak" || p.Region != "cn-hangzhou" {
		t.Fatalf("%+v", p)
	}
}
