// Package atomicfile replaces a file's content so that a reader, a kill, a power loss or a full disk at any point leaves the old content or the new content, never a truncated or half-written file.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/ownerfile"
)

var (
	// replaceTemp puts the finished temporary file in place of the target; tests inject the refusals a platform gives.
	replaceTemp = replace
	// copyAttributes carries the extended attributes of the file being replaced onto its replacement; tests inject the failures of a file system.
	copyAttributes = carryAttributes
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

// WriteFile replaces the file at path with data through a temporary file in the same directory: created private (never readable by anyone else, even
// for a moment), written, fsynced and renamed over the file, whose directory entry is then flushed. A failure at any step removes the temporary file
// and leaves the old file as it was. perm is the mode of a file that does not exist yet; an existing file keeps its mode.
//
// The rule: the file is replaced when that changes nothing but the inode; otherwise it is written in place, with the same bytes, exactly as Pi writes
// it (writeFileSync), once, and the in-place write is reported to [SetInPlaceReporter]. A symbolic link is followed to its target, as writeFileSync does,
// so the rename replaces the target and the link stays; the mode and, where the process may set them, the owner and group are kept, and so are the
// extended attributes of the file (on Linux they hold POSIX ACLs; on macOS the user's attributes), and on Windows the replaced file's DACL, attributes
// and streams. In place is the answer where
//   - the file has more than one hard link (a rename would leave the other names with the old bytes);
//   - an extended attribute cannot be copied for a reason other than a file system that keeps none (never dropped silently);
//   - the temporary file cannot be created in the directory because it refuses the process (EACCES, EROFS, EPERM: a directory the process cannot
//     write, holding a file it can); or the rename is refused (EBUSY, EXDEV, ETXTBSY: a file mounted into a container on its own is a mount point).
//
// A file the caller may not write (a read-only file) is refused as an in-place write would refuse it, and a write that fails for a real reason (a
// full disk, an I/O error) is an error, in place or not. On Windows a rename another process's open handle refuses is retried for
// [fsretry.RenameBudget] before its error is returned, and a refusal is never turned into an in-place write there.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
		path = resolved
	}
	mode := perm.Perm()
	var existing os.FileInfo
	//perf:ok one stat and one open per credential write, which already syncs a file
	if info, statErr := os.Stat(path); statErr == nil {
		existing = info
		mode = info.Mode().Perm()
		// Replacing a read-only file by rename would succeed where writing it fails.
		probe, openErr := os.OpenFile(path, os.O_WRONLY, 0)
		if openErr != nil {
			return openErr
		}
		links := linkCount(probe, info)
		_ = probe.Close()
		if links > 1 {
			return writeInPlace(path, data, perm, fmt.Errorf("the file has %d hard links, which a replacement would break", links))
		}
	}
	refusal, err := replaceThroughTemp(path, data, mode, existing)
	if refusal != nil {
		return writeInPlace(path, data, perm, refusal)
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

// replaceThroughTemp writes data to a temporary file beside path and puts it in place of path. refusal is non-nil, with the file untouched and the
// temporary file removed, where the platform refuses the replacement and the caller must write in place; err is a failure.
func replaceThroughTemp(path string, data []byte, mode os.FileMode, existing os.FileInfo) (refusal, err error) {
	temp, err := ownerfile.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		if refusesNewFile(err) {
			return err, nil
		}
		return nil, err
	}
	replaced := false
	defer func() {
		if !replaced {
			_ = temp.Close()
			_ = os.Remove(temp.Name())
		}
	}()
	if existing != nil {
		if err = carryIdentity(temp, existing); err != nil {
			return nil, err
		}
		if err = copyAttributes(temp, path); err != nil {
			return fmt.Errorf("an extended attribute cannot be carried over: %w", err), nil
		}
	}
	// The mode comes last: an ACL set before it fixes the group bits, and chown can clear set-id bits.
	if err = temp.Chmod(mode); err != nil {
		return nil, err
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
		if refusesReplace(err) {
			return err, nil
		}
		return nil, err
	}
	replaced = true
	// The rename is durable once its directory entry is; a platform that cannot open or sync a directory has nothing more to give.
	syncDirectory(filepath.Dir(path))
	return nil, nil
}

// syncDirectory flushes a directory's entries and reports whether it could; the write is complete either way.
func syncDirectory(path string) bool {
	dir, err := os.Open(path)
	if err != nil {
		return false
	}
	synced := dir.Sync() == nil
	_ = dir.Close()
	return synced
}
