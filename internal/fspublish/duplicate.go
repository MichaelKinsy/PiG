package fspublish

import (
	"io"
	"os"
)

// Duplicate makes the file at src also available at dst, which must not exist. It links src to dst. On Linux and Android, where the link is refused (EPERM or EACCES, as in Termux), it copies src to a new file at dst, sets src's permission bits on it whatever the umask, and syncs it. A failed copy removes the file it created.
func Duplicate(src, dst string) error {
	err := link(src, dst)
	if err == nil || !linkRefused(err) {
		return err
	}
	return copyNew(src, dst, true)
}

// LinkOrCopy makes the file at src also available at dst, which must not exist. It links src to dst and, when the link fails for any reason (a refused link, another file system, a platform without hard links), copies src to a new file at dst with src's permission bits whatever the umask. Unlike Duplicate, it does not sync the copy: its callers stage trees of thousands of files (a Go module source, the Node runtime), and a sync per file doubles the staging time. A copy that fails because dst exists reports that error.
func LinkOrCopy(src, dst string) error {
	if link(src, dst) == nil {
		return nil
	}
	return copyNew(src, dst, false)
}

// syncFile is (*os.File).Sync; tests replace it to observe which copies sync.
var syncFile = (*os.File).Sync

// copyNew copies src to dst, which it creates exclusively, and syncs it when durable is set.
func copyNew(src, dst string, durable bool) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	// The create mode is masked by the umask (often 077 for Android apps); fchmod is not.
	err = out.Chmod(info.Mode().Perm())
	if err == nil {
		_, err = io.Copy(out, in)
	}
	if err == nil && durable {
		err = syncFile(out)
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return err
}
