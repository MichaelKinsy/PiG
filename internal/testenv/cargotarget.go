package testenv

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// SharedCargoTargetDir returns a persistent Cargo target directory for one test package, so the Rust SDK's dependencies compile once per machine instead of once per package run. The directory lives under $PIG_TEST_CARGO_CACHE, else the user cache directory (pig-dev/cargo-target), and is named for the package and a hash of the given lockfiles and manifests, so a dependency change selects a fresh directory and an output built from another lock can never be reused.
//
// Cargo takes its own lock on a target directory, so concurrent builds of it wait for each other. Each test package gets its own directory because a cell's binary is copied out of release/<name> after its build: two packages building different cells of the same package name must not overwrite each other's output between build and copy. Within a package the directory was already shared by every test in the run.
func SharedCargoTargetDir(pkg string, lockInputs ...string) (string, error) {
	base := os.Getenv("PIG_TEST_CARGO_CACHE")
	if base == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("locate the user cache directory: %w", err)
		}
		base = filepath.Join(cache, "pig-dev", "cargo-target")
	}
	sum := sha256.New()
	for _, path := range lockInputs {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read Cargo lock input: %w", err)
		}
		_, _ = fmt.Fprintf(sum, "%s\x00%d\x00", filepath.Base(path), len(data))
		_, _ = sum.Write(data)
	}
	dir := filepath.Join(base, pkg+"-"+hex.EncodeToString(sum.Sum(nil))[:16])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create the shared Cargo target: %w", err)
	}
	return dir, nil
}
