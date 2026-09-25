//go:build unix

package ai

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// authFileRevision identifies one on-disk version of auth.json. Mirrors
// upstream utils/paths.ts getFileRevision: device, inode, size, and the
// nanosecond modification and status-change times.
func authFileRevision(path string) (string, bool) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return "", false
	}
	return fmt.Sprintf("%d:%d:%d:%d:%d", st.Dev, st.Ino, st.Size, st.Mtim.Nano(), st.Ctim.Nano()), true
}
