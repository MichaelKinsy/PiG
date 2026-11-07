// SPDX-License-Identifier: MIT

package testenv

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

const (
	// cellCacheKeep is how many directories of other hashes survive beside the current one: the branches a developer moves between. A full suite builds
	// 2 to 3 GB of cells into a directory, so the number bounds the disk the cache takes.
	cellCacheKeep = 2
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
// The calling process holds a shared lock on <dir>/.used until it exits. Of the directories of other hashes of the same package only the two most recently
// used survive (pruneCellCaches), and one that any process holds is never removed, however old.
func SharedCellCacheDir(pkg string, roots ...string) (string, error) {
	base := os.Getenv("PIG_TEST_CELL_CACHE")
	if base == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("locate the user cache directory: %w", err)
		}
		base = filepath.Join(cache, "pig-dev", "cell-cache")
	}
	sum := sha256.New()
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return "", fmt.Errorf("resolve cell cache input: %w", err)
		}
		_, _ = fmt.Fprintf(sum, "root\x00%d\x00%s\x00", len(abs), abs)
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
	dir := filepath.Join(base, pkg+"-"+hex.EncodeToString(sum.Sum(nil))[:16])
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

// pruneCellCaches removes the directories of other hashes of package pkg beyond the cellCacheKeep most recently used, never current and never one that a
// process holds: each removal first takes the directory's .used exclusively and without waiting, which a running process's shared lock refuses. A change to a
// hashed file selects a new directory and leaves the old one behind; without this they pile up, each holding every cell the suite builds. The scan holds a
// file lock, so processes that start together prune once, and a scan that finds it held leaves the work to the holder. Pruning is housekeeping: a
// directory that cannot be removed stays for the next scan.
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
	named := regexp.MustCompile("^" + regexp.QuoteMeta(pkg) + "-[0-9a-f]{16}$")
	type candidate struct {
		path string
		used time.Time
	}
	var others []candidate
	for _, entry := range entries {
		path := filepath.Join(base, entry.Name())
		if !entry.IsDir() || !named.MatchString(entry.Name()) || path == current {
			continue
		}
		info, err := os.Stat(filepath.Join(path, cellCacheUsed))
		if err != nil {
			if info, err = entry.Info(); err != nil {
				continue
			}
		}
		others = append(others, candidate{path, info.ModTime()})
	}
	slices.SortFunc(others, func(a, b candidate) int { return b.used.Compare(a.used) })
	for i, c := range others {
		if i < cellCacheKeep {
			continue
		}
		claim := flock.New(filepath.Join(c.path, cellCacheUsed))
		if locked, err := claim.TryLock(); err != nil || !locked {
			continue // a process is using it
		}
		_ = os.RemoveAll(c.path)
		_ = claim.Unlock()
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
