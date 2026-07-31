// Package atomicfile writes files atomically: data is written to a temp
// file in the target directory, then renamed into place, so a concurrent
// reader never sees a half-written file.
package atomicfile

import (
	"os"
	"path/filepath"
)

// WriteFile atomically replaces path's contents with data, creating
// path's directory if it doesn't already exist.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename below succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
