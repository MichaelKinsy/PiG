//go:build linux

package fspublish

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// renameat2 is unix.Renameat2; tests replace it to report a missing RENAME_NOREPLACE.
var renameat2 = unix.Renameat2

// linkRefused reports a link that the file system or its security policy does not permit, as opposed to an existing target or a missing stage.
func linkRefused(err error) bool {
	return errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES)
}

// renameNoReplace renames stage onto target with RENAME_NOREPLACE. Without RENAME_NOREPLACE in the kernel or file system it reports linkErr, the refused link.
func renameNoReplace(stage, target string, linkErr error) error {
	err := renameat2(unix.AT_FDCWD, stage, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL):
		return linkErr
	default:
		return &os.LinkError{Op: "rename", Old: stage, New: target, Err: err}
	}
}
