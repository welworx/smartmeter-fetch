package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFile_CreatesDirAndFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "out.txt")
	if err := WriteFile(path, []byte("hello")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}
}

func TestWriteFile_ReplacesExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := WriteFile(path, []byte("first")); err != nil {
		t.Fatalf("WriteFile (first): %v", err)
	}
	if err := WriteFile(path, []byte("second")); err != nil {
		t.Fatalf("WriteFile (second): %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "second" {
		t.Errorf("content = %q, want %q", got, "second")
	}
}

func TestWriteFile_MkdirAllError(t *testing.T) {
	// A regular file where a path component is expected to be a directory:
	// os.MkdirAll fails (ENOTDIR), no permission tricks needed.
	base := t.TempDir()
	regularFile := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(regularFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(regularFile, "sub", "out.txt")
	if err := WriteFile(path, []byte("x")); err == nil {
		t.Fatal("expected error creating parent dir under a regular file")
	}
}

func TestWriteFile_CreateTempError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // let t.TempDir() clean up
	path := filepath.Join(dir, "out.txt")
	if err := WriteFile(path, []byte("x")); err == nil {
		t.Fatal("expected error creating temp file in a read-only dir")
	}
}

func TestWriteFile_NoLeftoverTempFiles(t *testing.T) {
	dir := t.TempDir()
	if err := WriteFile(filepath.Join(dir, "out.txt"), []byte("x")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "out.txt" {
		t.Errorf("dir entries = %+v, want only out.txt", entries)
	}
}
