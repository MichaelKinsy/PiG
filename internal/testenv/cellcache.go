// SPDX-License-Identifier: MIT

package testenv

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const (
	// cellCacheRetention is how long a directory of another hash survives without a process using it.
	cellCacheRetention = 14 * 24 * time.Hour
	// cellCacheKeep is how many directories of other hashes survive however old they are: the branches a developer moves between.
	cellCacheKeep = 4
	// cellCacheUsed is the file whose modification time says when a process last used the directory.
	cellCacheUsed = ".used"
)

// SharedCellCacheDir returns a persistent directory for what the extension cells of a test package need to be shared between its go test processes: the
// config root's cache (a Host's cache/ext and cache/cells) and the sources the tests write for those cells. A package that runs as several sharded processes
// otherwise compiles every Rust cell and saves every Lisp image again in each one, because a cell's cache key names its source directory and a per-process
// temporary directory differs in every process.
//
// The directory lives under $PIG_TEST_CELL_CACHE, else the user cache directory (pig-dev/cell-cache), and is named for the package and a hash of the absolute
// path and the contents of every file under the given roots: the sources a cell's build reads beyond its own content address (the host's cell builders, the
// SDKs, the fixtures) and the test files that write fixture sources under a fixed content hash. A change to any of them selects a new directory, so no cell
// built by older code is reused. The cell cache's own publisher (runtimecell.PublishArtifact) is content-addressed, builds under a per-entry file lock and
// publishes with one rename, so processes that run at once and processes that start after one another share it safely. The absolute path is part of the key
// for the reason SharedCargoTargetDir's is: a build bakes in the paths of its sources. Directories of other hashes that no process has used for 14 days are
// removed (pruneCellCaches), except the four most recently used.
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create the shared cell cache: %w", err)
	}
	now := time.Now()
	used := filepath.Join(dir, cellCacheUsed)
	if err := os.WriteFile(used, nil, 0o644); err != nil {
		return "", fmt.Errorf("mark the shared cell cache used: %w", err)
	}
	if err := os.Chtimes(used, now, now); err != nil {
		return "", fmt.Errorf("mark the shared cell cache used: %w", err)
	}
	pruneCellCaches(base, pkg+"-", dir, now)
	return dir, nil
}

// pruneCellCaches removes the directories of other hashes of the same package that no process has used for cellCacheRetention, keeping the cellCacheKeep
// most recently used whatever their age and never touching current. A change to a hashed file selects a new directory and leaves the old one behind;
// without this they pile up, each holding every cell the suite builds. The scan holds a file lock, so processes that start together prune once, and a
// scan that finds the lock held leaves the work to the holder. Pruning is housekeeping: a directory that cannot be removed stays for the next scan.
func pruneCellCaches(base, prefix, current string, now time.Time) {
	lock := flock.New(filepath.Join(base, prefix+"prune.lock"))
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
	var others []candidate
	for _, entry := range entries {
		path := filepath.Join(base, entry.Name())
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) || path == current {
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
		if i >= cellCacheKeep && now.Sub(c.used) > cellCacheRetention {
			_ = os.RemoveAll(c.path)
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
