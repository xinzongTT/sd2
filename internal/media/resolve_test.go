package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveUUID(t *testing.T) {
	id := "c700d74e-b422-46b6-a6ca-c16a61cbbb02"
	got, err := Resolve(t.TempDir(), id)
	if err != nil || got != id {
		t.Fatalf("uuid: %v %q", err, got)
	}
}

func TestResolveDataURL(t *testing.T) {
	dir := t.TempDir()
	// tiny 1x1 png base64
	data := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	path, err := Resolve(dir, data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, ".png") {
		t.Fatalf("ext: %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestResolveLocal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(dir, p)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("empty")
	}
}

func TestSaveUpload(t *testing.T) {
	dir := t.TempDir()
	path, err := SaveUpload(dir, "hello.JPG", []byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.ToLower(path), ".jpg") {
		t.Fatalf("path %s", path)
	}
}
