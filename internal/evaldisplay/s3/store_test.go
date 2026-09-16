package s3

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"
)

func TestMemoryConcurrentPutGet(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		i := i
		go func() {
			key := "jobs/x/part.txt"
			err := m.Put(ctx, key, bytes.NewReader([]byte("ok")), 2, "text/plain")
			done <- err
		}()
		_ = i
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	rc, err := m.Get(ctx, "jobs/x/part.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	if string(b) != "ok" {
		t.Fatalf("got %q", b)
	}
}

func TestFSRoundTrip(t *testing.T) {
	fs, err := NewFS(filepath.Join(t.TempDir(), "b"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := fs.Put(ctx, "jobs/a/job.tar.gz", bytes.NewReader([]byte("gzip")), 4, "application/gzip"); err != nil {
		t.Fatal(err)
	}
	ok, size, err := fs.Head(ctx, "jobs/a/job.tar.gz")
	if err != nil || !ok || size != 4 {
		t.Fatalf("head %v %v %v", ok, size, err)
	}
	if _, err := fs.Get(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound got %v", err)
	}
}
