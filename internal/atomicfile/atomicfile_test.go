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
