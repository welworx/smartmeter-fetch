package main

import (
	"path/filepath"
	"testing"
)

func TestPointIndex_OrdinalAssignsInOrderOfFirstAppearance(t *testing.T) {
	dir := t.TempDir()
	idx, err := loadPointIndex(dir, "evn")
	if err != nil {
		t.Fatalf("loadPointIndex: %v", err)
	}

	n1, err := idx.ordinal("AT001")
	if err != nil {
		t.Fatalf("ordinal(AT001): %v", err)
	}
	if n1 != 1 {
		t.Errorf("ordinal(AT001) = %d, want 1", n1)
	}

	n2, err := idx.ordinal("AT002")
	if err != nil {
		t.Fatalf("ordinal(AT002): %v", err)
	}
	if n2 != 2 {
		t.Errorf("ordinal(AT002) = %d, want 2", n2)
	}

	// Re-querying an already-seen ID returns the same number, not a new one.
	again, err := idx.ordinal("AT001")
	if err != nil {
		t.Fatalf("ordinal(AT001) again: %v", err)
	}
	if again != 1 {
		t.Errorf("ordinal(AT001) again = %d, want 1", again)
	}
}

func TestPointIndex_PersistsAcrossLoads(t *testing.T) {
	dir := t.TempDir()
	idx, err := loadPointIndex(dir, "evn")
	if err != nil {
		t.Fatalf("loadPointIndex: %v", err)
	}
	if _, err := idx.ordinal("AT001"); err != nil {
		t.Fatalf("ordinal: %v", err)
	}
	if _, err := idx.ordinal("AT002"); err != nil {
		t.Fatalf("ordinal: %v", err)
	}

	reloaded, err := loadPointIndex(dir, "evn")
	if err != nil {
		t.Fatalf("loadPointIndex (reload): %v", err)
	}
	n, err := reloaded.ordinal("AT002")
	if err != nil {
		t.Fatalf("ordinal(AT002) after reload: %v", err)
	}
	if n != 2 {
		t.Errorf("ordinal(AT002) after reload = %d, want 2 (must not renumber)", n)
	}
}

func TestPointIndex_ScopedPerProvider(t *testing.T) {
	dir := t.TempDir()
	evnIdx, err := loadPointIndex(dir, "evn")
	if err != nil {
		t.Fatalf("loadPointIndex(evn): %v", err)
	}
	if _, err := evnIdx.ordinal("AT001"); err != nil {
		t.Fatalf("ordinal: %v", err)
	}

	otherIdx, err := loadPointIndex(dir, "other")
	if err != nil {
		t.Fatalf("loadPointIndex(other): %v", err)
	}
	n, err := otherIdx.ordinal("AT001")
	if err != nil {
		t.Fatalf("ordinal: %v", err)
	}
	if n != 1 {
		t.Errorf("ordinal(AT001) in a fresh provider index = %d, want 1 (index is per-provider)", n)
	}
}

func TestPointIndex_StoredAtExpectedPath(t *testing.T) {
	dir := t.TempDir()
	idx, err := loadPointIndex(dir, "evn")
	if err != nil {
		t.Fatalf("loadPointIndex: %v", err)
	}
	if _, err := idx.ordinal("AT001"); err != nil {
		t.Fatalf("ordinal: %v", err)
	}
	want := filepath.Join(dir, "evn", ".point-index.json")
	if idx.path != want {
		t.Errorf("idx.path = %q, want %q", idx.path, want)
	}
}
