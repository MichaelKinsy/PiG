//go:build !windows

package pigdocs

import "golang.org/x/sys/unix"

// canCreateIn reports whether this process may create entries in the existing directory dir: write and search permission on a writable file
// system (access(2) reports EROFS for a read-only mount and honors ACLs).
func canCreateIn(dir string) bool {
	return unix.Access(dir, unix.W_OK|unix.X_OK) == nil
}
