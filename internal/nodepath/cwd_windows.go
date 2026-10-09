//go:build windows

package nodepath

import "os"

// processCwd is process.cwd(); Windows has no $PWD indirection.
func processCwd() (string, error) { return os.Getwd() }
