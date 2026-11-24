// Package atomicfile replaces a file's content so that a reader, a kill, a power loss or a full disk at any point leaves the old content or the new content, never a truncated or half-written file.
package atomicfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
)

var (
	// replaceTemp puts the finished temporary file in place of the target; tests inject the refusals a platform gives.
	replaceTemp = replace
	// copyAttributes carries the extended attributes of the file being replaced onto its replacement; tests inject the failures of a file system.
	copyAttributes = carryAttributes
	// replaceRefused classifies a replaceTemp error as a platform refusal that falls back to an in-place write; tests substitute a platform classifier.
	replaceRefused = refusesReplace
)

// inPlaceReporter is told of every write that went in place; the embedding program points it at its debug log.
var inPlaceReporter atomic.Pointer[func(path string, cause error)]

// SetInPlaceReporter sets the function WriteFile calls, once, for a file it wrote in place instead of replacing, with the file and the reason; nil removes it. A program sets it once at start-up.
func SetInPlaceReporter(report func(path string, cause error)) {
	if report == nil {
		inPlaceReporter.Store(nil)
		return
	}
	inPlaceReporter.Store(&report)
}

// WriteFile replaces the file at path with data through a temporary file in the same directory: written, fsynced and renamed over the file, whose
// directory entry is then flushed. A failure at any step removes the temporary file and leaves the old file as it was. perm is the mode of a file that
// does not exist yet, which it gets as open(2) gives it (the process umask taken out, as in Pi's writeFileSync); an existing file keeps its mode.
// The temporary file of an existing file is created private (never readable by anyone else, even for a moment) and given the file's mode at the end.
//
// The rule: the file is replaced when that changes nothing but the inode; otherwise it is written in place, with the same bytes, exactly as Pi writes
// it (writeFileSync), once, and the in-place write is reported to [SetInPlaceReporter]. A symbolic link is followed to its target, as writeFileSync does,
// also to a target that does not exist yet (a chain of links ends at the file the last one names, which is created; a loop is ELOOP), so the rename
// replaces the target and the link stays; the mode and, where the process may set them, the owner and group are kept, and so are the
// extended attributes of the file (on Linux they hold POSIX ACLs; on macOS the user's attributes), and on Windows the replaced file's DACL entries with
// their flags and SE_DACL_PROTECTED (and its owner and group where the token may set them), user-settable attributes, and its SE_DACL_AUTO_INHERITED flag
// where the file system takes it back (D651). In place is the answer where
//   - the file has more than one hard link (a rename would leave the other names with the old bytes);
//   - an extended attribute, or on Windows the DACL's entries or SE_DACL_PROTECTED, cannot be given to the temporary file for a reason other than a file
//     system that keeps none (never dropped silently);
//   - the temporary file cannot be created in the directory because it refuses the process (EACCES, EROFS, EPERM: a directory the process cannot
//     write, holding a file it can); or the rename is refused (EBUSY, EXDEV, ETXTBSY: a file mounted into a container on its own is a mount point;
//     on Windows, ERROR_ACCESS_DENIED, ERROR_SHARING_VIOLATION or ERROR_LOCK_VIOLATION after [fsretry.RenameBudget] is spent);
//   - the file is not a regular file (a device such as /dev/null, a named pipe): a rename would replace the node itself with an ordinary file that has its mode.
//
// A file the caller may not write (a read-only file) is refused as an in-place write would refuse it, and a write that fails for a real reason (a
// full disk, an I/O error) is an error, in place or not. On Windows a rename another process's open handle refuses is retried for
// [fsretry.RenameBudget]; if the file is still held busy when the budget is spent, WriteFile writes in place as Pi writes it (reporting the cause and
// removing the temporary file). Any [*fs.PathError] naming the resolved target or its temporary sibling reports path as given, as os.WriteFile and
// Node's writeFileSync do.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	// pig divergence (D651): replaced through a private temporary file and a rename, in place where that is refused; Pi writes the file in place.
	given := path
	target, err := followLinks(given)
	if err != nil {
		return err
	}
	mode := perm.Perm()
	var existing os.FileInfo
	//perf:ok one stat and one open per credential write, which already syncs a file
	if info, statErr := os.Stat(target); statErr == nil {
		// A device or a named pipe is written, not replaced: a rename would put an ordinary file where the node was, with the node's mode, and the bytes would never reach it.
		if !info.Mode().IsRegular() {
			return writeInPlace(given, data, perm, fmt.Errorf("the file is a %s, which a replacement would remove", info.Mode().Type()))
		}
		existing = info
		mode = info.Mode().Perm()
		// Replacing a read-only file by rename would succeed where writing it fails.
		probe, openErr := os.OpenFile(target, os.O_WRONLY, 0)
		if openErr != nil {
			return withGivenPath(openErr, given, target)
		}
		links := linkCount(probe, info)
		_ = probe.Close()
		if links > 1 {
			return writeInPlace(given, data, perm, fmt.Errorf("the file has %d hard links, which a replacement would break", links))
		}
	}
	refusal, err := replaceThroughTemp(target, data, mode, existing)
	if refusal != nil {
		return writeInPlace(given, data, perm, refusal)
	}
	return withGivenPath(err, given, target)
}

// withGivenPath returns a fresh *fs.PathError naming given when err is an *fs.PathError for target or its temporary sibling beside target.
func withGivenPath(err error, given, target string) error {
	pathErr, ok := errors.AsType[*fs.PathError](err)
	if !ok {
		return err
	}
	if pathErr.Path == given {
		return err
	}
	if pathErr.Path == target || (filepath.Dir(pathErr.Path) == filepath.Dir(target) && strings.HasPrefix(pathErr.Path, target+".tmp-")) {
		return &fs.PathError{Op: pathErr.Op, Path: given, Err: pathErr.Err}
	}
	return err
}

// writeInPlace is Pi's writeFileSync: open (created with perm when it does not exist, an existing file keeps its mode), truncate, write.
func writeInPlace(path string, data []byte, perm os.FileMode, cause error) error {
	if report := inPlaceReporter.Load(); report != nil {
		(*report)(path, cause)
	}
	return os.WriteFile(path, data, perm)
}

// replaceThroughTemp writes data to a temporary file beside path and puts it in place of path. mode is the mode of the file being replaced, or the mode
// asked for a file that does not exist yet. refusal is non-nil, with the file untouched and the temporary file removed, where the platform refuses the
// replacement and the caller must write in place; err is a failure.
func replaceThroughTemp(path string, data []byte, mode os.FileMode, existing os.FileInfo) (refusal, err error) {
	temp, err := createTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*", mode, existing != nil)
	if err != nil {
		if refusesNewFile(err) {
			return err, nil
		}
		return nil, err
	}
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = temp.Close()
			_ = os.Remove(temp.Name())
		}
	}()
	if existing != nil {
		if err = carryIdentity(temp, existing); err != nil {
			return nil, err
		}
		if err = copyAttributes(temp, path); err != nil {
			return fmt.Errorf("an attribute of the file cannot be carried over: %w", err), nil
		}
		// The mode comes last: an ACL set before it fixes the group bits, and chown can clear set-id bits. A new file already has the mode open gave it.
		if err = temp.Chmod(mode); err != nil {
			return nil, err
		}
	}
	if _, err = temp.Write(data); err != nil {
		return nil, err
	}
	if err = temp.Sync(); err != nil {
		return nil, err
	}
	if err = temp.Close(); err != nil {
		return nil, err
	}
	if err = replaceTemp(temp.Name(), path, existing != nil); err != nil {
		if replaceRefused(err) {
			return err, nil
		}
		return nil, err
	}
	keepTemp = true // the temporary file is the target now
	// The rename is durable once its directory entry is. The file is replaced either way; a flush that fails for a real reason is the caller's to know.
	if err = syncDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return nil, nil
}

// maxLinkHops is how many symbolic links the kernel follows in a path before it answers ELOOP (Linux and macOS allow 40 and 32).
const maxLinkHops = 32

// followLinks is the file writeFileSync writes for path: every symbolic link is followed, to the file or, where the file does not exist, to the path the
// last link names, which the write creates; the links stay. A link that goes round in a circle is ELOOP, and one that names a file in a directory that
// does not exist is ENOENT where the file is created, as writeFileSync fails: neither is turned into a regular file.
func followLinks(path string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	}
	followed := path
	for range maxLinkHops {
		info, err := os.Lstat(followed)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			return followed, nil
		}
		target, err := os.Readlink(followed)
		if err != nil {
			return path, nil
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(followed), target)
		}
		followed = target
	}
	return "", &os.PathError{Op: "open", Path: path, Err: syscall.ELOOP}
}
