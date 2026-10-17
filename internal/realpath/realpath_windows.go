package realpath

import "path/filepath"

// EvalSymlinks is filepath.EvalSymlinks.
func EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
