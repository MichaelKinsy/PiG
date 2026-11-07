// SPDX-License-Identifier: MIT

package testenv

import (
	"crypto/sha256"
	"encoding/hex"
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
	// cellCacheKeep is how many directories of other hashes each checkout keeps beside its current one: the branches a developer moves between. A full suite
	// builds 2 to 3 GB of cells into a directory, so the number bounds the disk a checkout takes.
	cellCacheKeep = 2
	// cellCacheStale is how long a directory may go unused before it is removed whatever its checkout keeps: the cache of a checkout that no longer exists.
	cellCacheStale = 14 * 24 * time.Hour
	// cellCacheUsed is the file every process using a directory holds a shared lock on for its lifetime, and whose modification time says when one last
	// started on it.
	cellCacheUsed = ".used"
)

// cellCacheClaims holds the locks of the directories this process uses until it exits (the kernel releases a lock with its process, so a killed process
// never leaves a directory claimed).
var (
	cellCacheClaimsMu sync.Mutex
	cellCacheClaims   []*flock.Flock
)

// SharedCellCacheDir returns a persistent directory for what the extension cells of a test package need to be shared between its go test processes: the
// config root's cache (a Host's cache/ext and cache/cells) and the sources the tests write for those cells. A package that runs as several sharded processes
// otherwise compiles every Rust cell and saves every Lisp image again in each one, because a cell's cache key names its source directory and a per-process
// temporary directory differs in every process.
//
// The directory lives under $PIG_TEST_CELL_CACHE, else the user cache directory (pig-dev/cell-cache), and is named for the package and a hash of the absolute
// path and the contents of every file under the given roots (test files included, if a root holds them): the sources a cell's build reads beyond its own
// content address, such as the host's cell builders and the SDKs. A change to any of them selects a new directory, so no cell built by older code is reused.
// The cell cache's own publisher (runtimecell.PublishArtifact) is content-addressed, builds under a per-entry file lock and publishes with one rename, so
// processes that run at once and processes that start after one another share it safely. The absolute path is part of the key for the reason
// SharedCargoTargetDir's is: a build bakes in the paths of its sources.
//
// The name is <pkg>-<hash of the input paths>-<hash of their contents>: the first part names the checkout. The calling process holds a shared lock on
// <dir>/.used until it exits. Each checkout keeps its two most recently used directories of other contents, a directory unused for 14 days goes whatever its
// checkout keeps, and a directory that any process holds is never removed (pruneCellCaches).
func SharedCellCacheDir(pkg string, roots ...string) (string, error) {
	base := os.Getenv("PIG_TEST_CELL_CACHE")
	if base == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("locate the user cache directory: %w", err)
		}
		base = filepath.Join(cache, "pig-dev", "cell-cache")
	}
	sum, paths := sha256.New(), sha256.New()
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return "", fmt.Errorf("resolve cell cache input: %w", err)
		}
		_, _ = fmt.Fprintf(sum, "root\x00%d\x00%s\x00", len(abs), abs)
		_, _ = fmt.Fprintf(paths, "%d\x00%s\x00", len(abs), abs)
		err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != abs && skipCellCacheDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(abs, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			_, _ = fmt.Fprintf(sum, "%d\x00%s\x00%d\x00", len(rel), rel, len(data))
			_, _ = sum.Write(data)
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("hash cell cache input %s: %w", abs, err)
		}
	}
	checkout := hex.EncodeToString(paths.Sum(nil))[:16]
	dir := filepath.Join(base, pkg+"-"+checkout+"-"+hex.EncodeToString(sum.Sum(nil))[:16])
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", fmt.Errorf("create the shared cell cache: %w", err)
	}
	if err := claimCellCache(dir); err != nil {
		return "", err
	}
	pruneCellCaches(base, pkg, dir)
	return dir, nil
}

// claimCellCache creates dir and takes a shared lock on its .used file for the life of the process. A pruner that holds the file exclusively is removing the
// directory, so the claim waits for it and, once the directory is gone or replaced, starts again on the new one.
func claimCellCache(dir string) error {
	used := filepath.Join(dir, cellCacheUsed)
	for range 8 {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create the shared cell cache: %w", err)
		}
		lock := flock.New(used)
		if err := lock.RLock(); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // a pruner moved the directory away between the MkdirAll and the open
			}
			return fmt.Errorf("claim the shared cell cache: %w", err)
		}
		held, err := lock.Stat()
		current, statErr := os.Stat(used)
		if err == nil && statErr == nil && os.SameFile(held, current) {
			now := time.Now()
			_ = os.Chtimes(used, now, now)
			cellCacheClaimsMu.Lock()
			cellCacheClaims = append(cellCacheClaims, lock)
			cellCacheClaimsMu.Unlock()
			return nil
		}
		_ = lock.Close() // the lock is on a file a pruner has removed
	}
	return fmt.Errorf("claim the shared cell cache %s: it was removed under the claim eight times", dir)
}

// cellCacheName reports whether name is <pkg>-<16 hex digits>-<16 hex digits> and returns the first group, the checkout.
func cellCacheName(name, pkg string) (checkout string, ok bool) {
	rest, found := strings.CutPrefix(name, pkg+"-")
	if !found || len(rest) != 33 || rest[16] != '-' {
		return "", false
	}
	for i := range len(rest) {
		if i != 16 && !lowerHex(rest[i]) {
			return "", false
		}
	}
	return rest[:16], true
}

func lowerHex(c byte) bool { return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' }

// pruneCellCaches removes directories of package pkg, never current and never one that a process holds: each removal first takes the directory's .used
// exclusively and without waiting, which a running process's shared lock refuses. A directory goes when it is beyond the cellCacheKeep most recently used
// of its checkout (the first hash of its name; current is not counted, so a checkout keeps current and the two before it), or when it has
// not been used for cellCacheStale. A change to a hashed file selects a new directory and leaves the old one behind, each holding every cell the suite builds;
// the checkouts of a machine that shares one cache root (containers, lanes, worktrees) must not evict one another, which a count over the whole root would.
// The scan holds a file lock, so processes that start together prune once, and a scan that finds it held leaves the work to the holder. Pruning is
// housekeeping: a directory that cannot be removed stays for the next scan (on Windows, which cannot rename a directory that holds a file open for locking,
// that is every directory: nothing is pruned there).
//
// A directory is removed by renaming it to a name that nothing matches (.<name>.removing) while the lock is held, then deleting that: deleting in place would
// unlink .used first and let a process that arrives meanwhile create a new .used, lock it at once and find its directory deleted under it.
func pruneCellCaches(base, pkg, current string) {
	lock := flock.New(filepath.Join(base, pkg+"-prune.lock"))
	if locked, err := lock.TryLock(); err != nil || !locked {
		return
	}
	defer func() { _ = lock.Unlock() }()
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	type candidate struct {
		path string
		used time.Time
	}
	byCheckout := map[string][]candidate{}
	for _, entry := range entries {
		path := filepath.Join(base, entry.Name())
		checkout, ok := cellCacheName(entry.Name(), pkg)
		if !entry.IsDir() || !ok || path == current {
			continue
		}
		info, err := os.Stat(filepath.Join(path, cellCacheUsed))
		if err != nil {
			if info, err = entry.Info(); err != nil {
				continue
			}
		}
		byCheckout[checkout] = append(byCheckout[checkout], candidate{path, info.ModTime()})
	}
	now := time.Now()
	for _, others := range byCheckout {
		slices.SortFunc(others, func(a, b candidate) int { return b.used.Compare(a.used) })
		for i, c := range others {
			if i < cellCacheKeep && now.Sub(c.used) <= cellCacheStale {
				continue
			}
			claim := flock.New(filepath.Join(c.path, cellCacheUsed))
			if locked, err := claim.TryLock(); err != nil || !locked {
				continue // a process is using it
			}
			tombstone := filepath.Join(base, "."+filepath.Base(c.path)+".removing")
			if err := os.RemoveAll(tombstone); err == nil && os.Rename(c.path, tombstone) == nil {
				_ = os.RemoveAll(tombstone)
			}
			_ = claim.Unlock()
		}
	}
}

// skipCellCacheDir names the directories no build reads: dependency and build output trees, which hold the machine's installs and the compilers' products.
func skipCellCacheDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "target", "vendor", "dist", "build", "__pycache__":
		return true
	}
	return false
}
