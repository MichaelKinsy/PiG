package runtimecell

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// VanishTracker decides, for a source hash walk, whether a directory that failed to open or list mid-walk was removed by another
// process and so held no source of the build. One tracker serves one walk: a directory it let the walk skip on weaker evidence is
// checked again by Verify once the walk ends.
//
// A directory that is gone is vanished. Windows does not report a directory that another process is deleting as missing: it
// fails to open with ERROR_DELETE_PENDING, or with ERROR_ACCESS_DENIED, until the last handle on it closes. So:
//   - not-exist, and ERROR_DELETE_PENDING, vanish the directory at once;
//   - ERROR_ACCESS_DENIED vanishes it at once when a Lstat of the same directory finds it gone;
//   - ERROR_ACCESS_DENIED followed by a Lstat that succeeds is a directory that is there and denies the walk: not vanished, so the
//     hash fails;
//   - ERROR_ACCESS_DENIED followed by a Lstat that is denied too is skipped provisionally and remembered. A delete-pending
//     directory denies both calls, so the walk cannot tell it from a live one yet, and skipping it unchecked would let the sources
//     of a live, unreadable tree drop out of the hash. Verify looks again once the walk is over: gone is fine; a Lstat that now
//     succeeds means the directory exists, and the hash fails naming it; still denied is taken as delete-pending, because a
//     directory that denies even its metadata to the user running the hash, and does not go away, is outside what a source hash can
//     read either way.
type VanishTracker struct {
	deletion func(error) (pending, denied bool)
	deferred []deferredDir
}

type deferredDir struct {
	name  string
	lstat func() error
}

// NewVanishTracker returns a tracker that knows the deletion codes of the running OS.
func NewVanishTracker() *VanishTracker { return &VanishTracker{deletion: windowsDeletion} }

// Vanished reports whether err, from opening or listing the directory dir of the local file system, means the walk may skip dir.
func (t *VanishTracker) Vanished(dir string, err error) bool {
	return t.vanished(dir, err, func() error { _, lerr := os.Lstat(dir); return lerr }) //perf:ok runs only for a directory that failed to open mid-walk
}

func (t *VanishTracker) vanished(name string, err error, lstat func() error) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	pending, denied := t.deletion(err)
	if pending {
		return true
	}
	if !denied {
		return false
	}
	lerr := lstat()
	if lerr == nil {
		return false
	}
	if errors.Is(lerr, fs.ErrNotExist) {
		return true
	}
	if lerrPending, lerrDenied := t.deletion(lerr); lerrPending || lerrDenied {
		t.deferred = append(t.deferred, deferredDir{name: name, lstat: lstat})
		return true
	}
	return false
}

// Verify re-checks the directories Vanished skipped provisionally and returns an error naming the first that exists. Call it once,
// after the walk, and fail the hash on the error.
func (t *VanishTracker) Verify() error {
	for _, dir := range t.deferred {
		lerr := dir.lstat()
		if lerr == nil {
			return fmt.Errorf("directory %s denied the source hash walk and still exists, so its sources were not hashed", dir.name)
		}
		if errors.Is(lerr, fs.ErrNotExist) {
			continue
		}
		if pending, denied := t.deletion(lerr); !pending && !denied {
			return fmt.Errorf("directory %s denied the source hash walk and cannot be checked afterwards: %w", dir.name, lerr)
		}
	}
	return nil
}
