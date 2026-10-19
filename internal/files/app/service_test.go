package app_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/files/app"
	"github.com/admin-frame/admin-frame/pkg/storage"
)

func TestUploadAndDownload(t *testing.T) {
	store := storage.NewMemory(storage.MemoryConfig{})
	defer store.Close()
	svc := app.NewService(store, app.Config{})

	info, err := svc.Upload(context.Background(), "export", "report.csv", "text/csv", strings.NewReader("a,b\n1,2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(info.Key, "files/export/") || !strings.Contains(info.Key, "report.csv") {
		t.Fatalf("key=%s", info.Key)
	}

	rc, got, err := svc.OpenDownload(context.Background(), info.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !bytes.Equal(body, []byte("a,b\n1,2\n")) || got.Key != info.Key {
		t.Fatalf("body=%q got=%+v", body, got)
	}
}

func TestRejectBadContentTypeAndPath(t *testing.T) {
	svc := app.NewService(storage.NewMemory(storage.MemoryConfig{}), app.Config{})
	_, err := svc.Upload(context.Background(), "x", "a.exe", "application/octet-stream", strings.NewReader("x"))
	if err == nil {
		t.Fatal("expected content_type reject")
	}
	_, _, err = svc.OpenDownload(context.Background(), "other/ns/a")
	if err == nil {
		t.Fatal("expected namespace reject")
	}
}

func TestPresignUnsupportedOnMemory(t *testing.T) {
	svc := app.NewService(storage.NewMemory(storage.MemoryConfig{}), app.Config{})
	_, err := svc.PresignUpload(context.Background(), "export", "a.csv", "text/csv")
	if err == nil {
		t.Fatal("expected unsupported")
	}
}
