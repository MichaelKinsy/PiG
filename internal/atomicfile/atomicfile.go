// Package atomicfile replaces a file's content so that a reader, a kill, a power loss or a full disk at any point leaves the old content or the new content, never a truncated or half-written file.
package atomicfile

import (
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/internal/ownerfile"
)

// WriteFile replaces the file at path with data through a temporary file in the same directory: created private (never readable by anyone else, even
// for a moment), written, fsynced and renamed over the file, whose directory entry is then flushed. A failure at any step removes the temporary file
// and leaves the old file as it was. perm is the mode of a file that does not exist yet; an existing file keeps its mode.
//
// A symbolic link is followed to its target, as writeFileSync does, so the rename replaces the target and the link stays. A file the caller may not
// write (a read-only file) is refused as an in-place write would refuse it. On Windows a rename another process's open handle refuses is retried for
// [fsretry.RenameBudget] before its error is returned.
//
// What a replacement does not carry over: a hard link to the old file keeps the old content, and extended attributes of the old file (Unix) are not copied.
// The mode, and the owner and group where the process may set them, are kept; on Windows the replaced file's DACL, attributes and streams are.
func WriteFile(path string, data []byte, perm os.FileMode) (err error) {
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
		_ = probe.Close()
	}
	temp, err := ownerfile.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = temp.Close()
			_ = os.Remove(temp.Name())
		}
	}()
	if err = temp.Chmod(mode); err != nil {
		return err
	}
	if existing != nil {
		if err = carryIdentity(temp, existing); err != nil {
			return err
		}
	}
	if _, err = temp.Write(data); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = replace(temp.Name(), path, existing != nil); err != nil {
		return err
	}
	// The rename is durable once its directory entry is; a platform that cannot open or sync a directory has nothing more to give.
	syncDirectory(filepath.Dir(path))
	return nil
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
