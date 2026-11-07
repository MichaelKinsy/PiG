// SPDX-License-Identifier: MIT

package testenv

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
// for the reason SharedCargoTargetDir's is: a build bakes in the paths of its sources.
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
	return dir, nil
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
