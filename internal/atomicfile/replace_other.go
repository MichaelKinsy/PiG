//go:build !windows

package atomicfile

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/fsretry"
	"github.com/MichaelKinsy/PiG/internal/ownerfile"
)

// createTemp creates the temporary file beside the target. The temporary file of an existing file is private (0600, as os.CreateTemp creates it) and
// is given the file's mode at the end. A file that does not exist yet is created as writeFileSync creates it, with open(O_CREAT|O_EXCL, perm): the kernel
// takes the process umask out of perm and gives the file the default ACL of its directory. Nothing here reads the umask: reading it sets it, and that races
// with every other thread that creates a file. The file is no more readable than it will stay, so it is never wider than the file the caller gets.
func createTemp(dir, pattern string, perm os.FileMode, existed bool) (*os.File, error) {
	if existed {
		return ownerfile.CreateTemp(dir, pattern)
	}
	prefix, suffix, _ := strings.Cut(pattern, "*")
	for range 10000 {
		file, err := os.OpenFile(filepath.Join(dir, prefix+strconv.FormatUint(uint64(rand.Uint32()), 10)+suffix), os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return file, err
	}
	return nil, &os.PathError{Op: "createtemp", Path: filepath.Join(dir, pattern), Err: os.ErrExist}
}

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
