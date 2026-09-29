//go:build !windows

package codingagent

import "path/filepath"

func evalCanonicalPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
