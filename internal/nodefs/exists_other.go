//go:build !windows

package nodefs

import "golang.org/x/sys/unix"

// Exists is fs.existsSync: Node asks access(2) for F_OK, which follows symbolic links and reads no metadata. Unlike os.Stat it
// allocates only the system call's copy of name: no FileInfo, and no PathError for a missing path.
func Exists(name string) bool {
	return unix.Access(name, unix.F_OK) == nil
}
