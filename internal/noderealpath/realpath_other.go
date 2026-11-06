//go:build !windows

package noderealpath

import "github.com/MichaelKinsy/PiG/internal/realpath"

// Eval is the link walk of fs.realpathSync over an absolute path.
func Eval(path string) (string, error) {
	return realpath.EvalSymlinks(path)
}
