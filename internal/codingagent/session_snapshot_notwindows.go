//go:build !windows

package codingagent

import "os"

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
