//go:build !windows

package nodefs

import "golang.org/x/sys/unix"

// Access is fs.accessSync(path, F_OK) and reports whether it succeeds. Unlike [Exists] it is the one existence check Node
// shares between platforms: on Unix both are access(2).
//
// Ports packages/coding-agent/src/utils/shell.ts (the accessSync(path, F_OK) existence check of shellPathExists).
func Access(name string) bool {
	return unix.Access(name, unix.F_OK) == nil
}
