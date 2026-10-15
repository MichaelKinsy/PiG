//go:build !windows

package codingagent

import (
	"errors"
	"os"
	"syscall"
)

// openDebugLog opens the shared debug log for appending without following a symlink and only when the current user owns
// the file and it has no other link, so another local user cannot redirect the log or read what it holds. The file is
// private (0600).
func openDebugLog(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_APPEND|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		_ = f.Close()
		return nil, err
	}
	// A second link to the file means it is also some other path, such as a file of this user that another local user
	// hard-linked into the shared directory.
	if stat.Mode&syscall.S_IFMT != syscall.S_IFREG || int(stat.Uid) != os.Getuid() || stat.Nlink != 1 {
		_ = f.Close()
		return nil, errors.New("debug log is not a regular file with one link owned by the current user")
	}
	// A log an earlier version created readable by others becomes private too.
	if stat.Mode&0o077 != 0 {
		if err := f.Chmod(0o600); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return f, nil
}
