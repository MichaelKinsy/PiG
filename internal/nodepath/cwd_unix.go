//go:build !windows

package nodepath

import "syscall"

// processCwd is process.cwd(): libuv's uv_cwd calls getcwd(3), the physical path. os.Getwd returns $PWD instead when it names the
// same directory, which keeps a symlink in the path that Node never reports.
func processCwd() (string, error) { return syscall.Getwd() }
