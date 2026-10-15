//go:build !windows

package codingagent

import (
	"os"
	"path/filepath"
)

// unlinkedTemp creates a file in dir and removes its name, so it lives only while open.
func unlinkedTemp(dir string) (*os.File, error) {
	file, err := os.CreateTemp(dir, ".pig-snapshot-*")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(file.Name()); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// snapshotDir is where a snapshot of the session file at path is created: beside it, where a clone of it can share its blocks. The
// snapshot has no name, so the directory never lists it.
func snapshotDir(path string) string { return filepath.Dir(path) }
