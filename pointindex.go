package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/welworx/smartmeter-fetch/internal/atomicfile"
)

// pointIndex assigns each real metering point ID a small, stable
// sequential number (1, 2, 3, ...) for use in -out path templates
// (<zaehlerpunkt_id>), so users can keep real Zählpunkt IDs out of local
// file paths if they'd rather. Numbering is assigned in order of first
// appearance and persisted per-provider (not per-profile — Store already
// keys stored data by provider+point only, not profile) at
// <data-dir>/<provider>/.point-index.json.
type pointIndex struct {
	path     string
	ordinals map[string]int
}

// loadPointIndex loads the persisted index for providerName under
// dataDir, or returns an empty one if none exists yet.
func loadPointIndex(dataDir, providerName string) (*pointIndex, error) {
	path := filepath.Join(dataDir, providerName, ".point-index.json")
	idx := &pointIndex{path: path, ordinals: map[string]int{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return idx, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &idx.ordinals); err != nil {
		return nil, err
	}
	return idx, nil
}

// ordinal returns pointID's persisted number, assigning and persisting
// the next one if pointID hasn't been seen before.
func (idx *pointIndex) ordinal(pointID string) (int, error) {
	if n, ok := idx.ordinals[pointID]; ok {
		return n, nil
	}
	n := len(idx.ordinals) + 1
	idx.ordinals[pointID] = n
	if err := idx.save(); err != nil {
		delete(idx.ordinals, pointID)
		return 0, err
	}
	return n, nil
}

func (idx *pointIndex) save() error {
	data, err := json.MarshalIndent(idx.ordinals, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(idx.path, data)
}
