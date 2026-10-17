//go:build unix

package ai

import (
	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/profiling"
)

// fileRevision is the identity authFileRevision reads. It is comparable, so a read that finds the file unchanged allocates nothing; the zero value is "no revision read".
type fileRevision struct{ dev, ino, size, mtime, ctime int64 }

// authFileRevision identifies one on-disk version of auth.json. Mirrors
// upstream utils/paths.ts getFileRevision: device, inode, size, and the
// nanosecond modification and status-change times.
func authFileRevision(path string) (fileRevision, bool) {
	profiling.FileRevisionStats.Add(1)
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return fileRevision{}, false
	}
	return fileRevision{int64(st.Dev), int64(st.Ino), st.Size, st.Mtim.Nano(), st.Ctim.Nano()}, true
}
