//go:build !windows

package codingagent

import "github.com/MichaelKinsy/PiG/internal/realpath"

func evalCanonicalPath(path string) (string, error) {
	return realpath.EvalSymlinks(path)
}
