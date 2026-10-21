// Package contentdirs keeps content-addressed directories under a shared config root: one directory per build of an embedded bundle, named
// <base>-<hash>. PiG builds that share a config root (two installed versions, or stock PiG beside a Piglet Binary) each read and write only their
// own directory, so no build rewrites another's files or strands its extension builds.
//
// A directory records its last use in its modification time. Eviction keeps the most recently used directories and never removes the caller's
// own, so a build that is still in use keeps its files. A process that reads a directory for as long as it lives, such as a session whose prompt
// names the pages of its documentation bundle, [Hold]s it: another build's eviction leaves a held directory, whatever its modification time.
package contentdirs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

const (
	// Keep is how many directories of one base survive eviction, the caller's own included. It covers a stock install, a Piglet Binary and an
	// upgrade in progress sharing one config root; each directory holds a few MiB.
	Keep = 4
	// touchInterval bounds how often a warm start writes a use time, so most starts write nothing.
	touchInterval = time.Hour
	// leaseSuffix names the file beside a directory that holders of the directory lock shared for as long as they live.
	leaseSuffix = ".lease"
	// hashLen is the length of the hex content hash in a directory name.
	hashLen = 16
)

// Name is the directory name of base's bundle with content hash, which must be at least 16 hex digits; only the first 16 are used.
func Name(base, hash string) (string, error) {
	if len(hash) < hashLen || !isHex(hash[:hashLen]) {
		return "", fmt.Errorf("content hash %q is not %d hex digits", hash, hashLen)
	}
	return base + "-" + hash[:hashLen], nil
}

// Touch records that dir was used now, unless it was recorded within the last hour. It is best-effort: a directory this user cannot write keeps
// its old time, which only makes it an earlier eviction candidate.
func Touch(dir string) {
	info, err := os.Stat(dir)
	if err != nil || time.Since(info.ModTime()) < touchInterval {
		return
	}
	now := time.Now()
	_ = os.Chtimes(dir, now, now)
}

// leases are the leases this process holds, by lease file.
var leases sync.Map

// Hold records that this process reads dir until the returned release runs, which a process that holds a directory for as long as it lives never calls: [Evict]
// leaves a held directory, in this process and in every other, whatever its use time. The operating system releases the lease with the process, so a holder
// that dies leaves nothing behind but a lease file the next eviction removes with the directory. A directory with no lease file has no holder, so a build that
// never holds one (an older PiG) is evicted as before. Hold is best-effort, like [Touch]: a parent this user cannot write leaves the directory evictable, and
// release is then a no-op. Holding a directory twice holds it once.
func Hold(dir string) (release func()) {
	path := dir + leaseSuffix
	if _, held := leases.Load(path); held {
		return func() {}
	}
	lease := flock.New(path)
	if acquired, err := lease.TryRLock(); err != nil || !acquired {
		return func() {}
	}
	if _, raced := leases.LoadOrStore(path, lease); raced {
		_ = lease.Unlock()
		return func() {}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			leases.Delete(path)
			_ = lease.Unlock()
		})
	}
}

// inUse reports whether a process holds dir. It takes the lease exclusively when there is one, which no holder allows, and returns the lease so the caller
// removes dir while it is held; the caller releases it.
func inUse(dir string) (lease *flock.Flock, held bool) {
	path := dir + leaseSuffix
	if _, err := os.Stat(path); err != nil {
		return nil, false
	}
	lease = flock.New(path)
	acquired, err := lease.TryLock()
	if err != nil {
		// A lease this process cannot open is no proof of a holder; removing the directory fails by itself where the parent is not writable.
		return nil, false
	}
	return lease, !acquired
}

// Dirs returns the paths of the directories in parent named <base>-<16 hex digits>, most recently used first.
func Dirs(parent, base string) ([]string, error) {
	entries, err := os.ReadDir(parent)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type dir struct {
		path string
		used time.Time
	}
	var dirs []dir
	for _, entry := range entries {
		hash, ok := strings.CutPrefix(entry.Name(), base+"-")
		if !ok || len(hash) != hashLen || !isHex(hash) || !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, dir{filepath.Join(parent, entry.Name()), info.ModTime()})
	}
	slices.SortFunc(dirs, func(a, b dir) int { return b.used.Compare(a.used) })
	paths := make([]string, len(dirs))
	for i, d := range dirs {
		paths[i] = d.path
	}
	return paths, nil
}

// Evict removes base's directories in parent beyond the [Keep] most recently used, never own, and returns the removed paths. A directory some process
// [Hold]s stays, and does not count among the kept ones. The caller holds whatever lock excludes readers of those directories.
func Evict(parent, base, own string) ([]string, error) {
	dirs, err := Dirs(parent, base)
	if err != nil {
		return nil, err
	}
	kept := 1
	var removed []string
	var errs []error
	for _, dir := range dirs {
		if dir == own {
			continue
		}
		if kept < Keep {
			kept++
			continue
		}
		lease, held := inUse(dir)
		if held {
			continue
		}
		err := os.RemoveAll(dir)
		if lease != nil {
			_ = lease.Unlock()
			if err == nil {
				_ = os.Remove(dir + leaseSuffix)
			}
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, dir)
	}
	return removed, errors.Join(errs...)
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
