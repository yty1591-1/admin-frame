package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

func TestValidateKeyAndJoin(t *testing.T) {
	if err := storage.ValidateKey(""); !errors.Is(err, storage.ErrInvalidKey) {
		t.Fatalf("err=%v", err)
	}
	if err := storage.ValidateKey("../x"); err == nil {
		t.Fatal("expected traversal reject")
	}
	if err := storage.ValidateKey("/abs"); err == nil {
		t.Fatal("expected abs reject")
	}
	key, err := storage.Join("m1", "orders", "a.pdf")
	if err != nil || key != "m1/orders/a.pdf" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	if _, err := storage.Join("m1", "..", "x"); err == nil {
		t.Fatal("expected bad segment")
	}
}

func TestMemoryPutGetDeleteList(t *testing.T) {
	ctx := context.Background()
	s := storage.NewMemory(storage.MemoryConfig{})
	defer func() { _ = s.Close() }()

	info, err := s.Put(ctx, "a/b.txt", strings.NewReader("hello"), storage.PutOptions{
		ContentType: "text/plain",
		Metadata:    map[string]string{"k": "v"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != 5 || info.ContentType != "text/plain" || info.ETag == "" || info.Metadata["k"] != "v" {
		t.Fatalf("info=%+v", info)
	}

	rc, got, err := s.Get(ctx, "a/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	body, _ := io.ReadAll(rc)
	if string(body) != "hello" || got.ETag != info.ETag {
		t.Fatalf("body=%s got=%+v", body, got)
	}

	ok, err := s.Exists(ctx, "a/b.txt")
	if err != nil || !ok {
		t.Fatalf("exists=%v err=%v", ok, err)
	}

	_, err = s.Put(ctx, "a/c.txt", strings.NewReader("x"), storage.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, storage.ListOptions{Prefix: "a/", Limit: 1})
	if err != nil || len(list.Items) != 1 || !list.IsTruncated {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	list2, err := s.List(ctx, storage.ListOptions{Prefix: "a/", Limit: 1, ContinuationToken: list.NextToken})
	if err != nil || len(list2.Items) != 1 {
		t.Fatalf("list2=%+v err=%v", list2, err)
	}

	if err := s.Delete(ctx, "a/b.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stat(ctx, "a/b.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestMemoryTooLarge(t *testing.T) {
	s := storage.NewMemory(storage.MemoryConfig{MaxObjectBytes: 4})
	defer func() { _ = s.Close() }()
	_, err := s.Put(context.Background(), "k", strings.NewReader("12345"), storage.PutOptions{})
	if !errors.Is(err, storage.ErrTooLarge) {
		t.Fatalf("err=%v", err)
	}
}

func TestMemoryPresignUnsupported(t *testing.T) {
	s := storage.NewMemory(storage.MemoryConfig{})
	defer func() { _ = s.Close() }()
	if _, err := s.PresignGet(context.Background(), "k", 0); !errors.Is(err, storage.ErrUnsupported) {
		t.Fatalf("err=%v", err)
	}
}

func TestLocalStore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := storage.OpenLocal(storage.LocalConfig{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	key := storage.MustJoin("tenant", "doc.pdf")
	info, err := s.Put(ctx, key, bytes.NewReader([]byte("%PDF")), storage.PutOptions{
		ContentType: "application/pdf",
		Metadata:    map[string]string{"src": "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.ContentType != "application/pdf" {
		t.Fatalf("ct=%s", info.ContentType)
	}

	st, err := s.Stat(ctx, key)
	if err != nil || st.Metadata["src"] != "test" {
		t.Fatalf("stat=%+v err=%v", st, err)
	}

	rc, _, err := s.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(b) != "%PDF" {
		t.Fatalf("body=%q", b)
	}

	// 路径穿越不得逃出 root
	if _, err := s.Put(ctx, "../escape", strings.NewReader("x"), storage.PutOptions{}); !errors.Is(err, storage.ErrInvalidKey) {
		t.Fatalf("err=%v", err)
	}
	abs := filepath.Join(root, "tenant", "doc.pdf")
	if _, err := os.Stat(abs); err != nil {
		t.Fatal(err)
	}

	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
}

func TestPrefixStore(t *testing.T) {
	ctx := context.Background()
	inner := storage.NewMemory(storage.MemoryConfig{})
	defer func() { _ = inner.Close() }()
	s, err := storage.WithPrefix(inner, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Put(ctx, "file.txt", strings.NewReader("z"), storage.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ok, err := inner.Exists(ctx, "tenant-a/file.txt")
	if err != nil || !ok {
		t.Fatalf("inner exists=%v err=%v", ok, err)
	}
	rc, info, err := s.Get(ctx, "file.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	if info.Key != "file.txt" {
		t.Fatalf("key=%s", info.Key)
	}
}
