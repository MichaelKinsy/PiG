//go:build !windows

package atomicfile

import (
	"context"
	"errors"
	"os"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/fsretry"
)

// carryIdentity gives temp the owner and group of the file it replaces, as an in-place rewrite would have kept them. Changing an owner needs privilege:
// a refusal (EPERM) means the process that rewrites the file is not the one that owns it, and the file then belongs to the rewriting user, who can
// read it back; any other failure is returned.
func carryIdentity(temp *os.File, replaced os.FileInfo) error {
	stat, ok := replaced.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if err := temp.Chown(int(stat.Uid), int(stat.Gid)); err != nil && !errors.Is(err, syscall.EPERM) {
		return err
	}
	return nil
}

// replace renames temp over path.
func replace(temp, path string, _ bool) error {
	//portlint:allow ctxbackground a credential write has no caller context: the rename must not be abandoned half way, and fsretry bounds it by RenameBudget
	return fsretry.Rename(context.Background(), temp, path, nil)
}
