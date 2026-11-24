package pioracle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// OverrideTreeEnv names the Pi an oracle test compares with when it is not the pin's: the tree `pin.py override-tree` builds in
// the build store for another Pi commit (automation/pin/vendor.py override_tree): `packages`, the packages of that commit's
// source build, and durable/interop/node_modules, an install of that build's packs. A run at another Pi commit sets it for the
// tests of Pi work the Go port carries ahead of the pin. The tree is read where it is: nothing of it is installed or linked
// into the repository, so a run that sets the variable leaves the repository's own Pi (the mirror, the oracle link,
// durable/interop/node_modules) as it was.
const OverrideTreeEnv = "PIG_PI_OVERRIDE_TREE"

// OverrideTree returns the tree OverrideTreeEnv names, or "" in a run at the pin. The test fails when the directory is not a
// finished override tree whose install holds the Pi Durable its own packs name.
func OverrideTree(t testing.TB) string {
	t.Helper()
	tree := os.Getenv(OverrideTreeEnv)
	if tree == "" {
		return ""
	}
	if err := checkOverrideTree(tree); err != nil {
		t.Fatal(err)
	}
	return tree
}

func checkOverrideTree(tree string) error {
	ready, err := os.ReadFile(filepath.Join(tree, ".ready"))
	if err != nil {
		return fmt.Errorf("pioracle: %s=%s is not a tree `pin.py override-tree` finished: %w", OverrideTreeEnv, tree, err)
	}
	if info, err := os.Stat(filepath.Join(tree, "packages", "durable", "src")); err != nil || !info.IsDir() {
		return fmt.Errorf("pioracle: %s=%s has no packages/durable/src", OverrideTreeEnv, tree)
	}
	return checkDurableOf(filepath.Join(tree, "durable", "interop", "node_modules"), tree, strings.TrimSpace(string(ready)), "")
}

// Packages returns the packages directory of the Pi source the oracle tests of this run load: the pin's mirror
// (.upstream/current/packages), or the packages of the tree OverrideTreeEnv names.
func Packages(t testing.TB) string {
	t.Helper()
	if tree := OverrideTree(t); tree != "" {
		return filepath.Join(tree, "packages")
	}
	return filepath.Join(repoRoot(t), ".upstream", "current", "packages")
}

// Project returns the directory, with its trailing separator, whose node_modules holds the built Pi packages the oracle tests
// of this run import (the oracle scripts read it as PI_INTEROP_DIR): the repository's durable/interop, where make
// durable-interop-deps installs the pin's vendored packs, or the durable/interop of the tree OverrideTreeEnv names.
func Project(t testing.TB) string {
	t.Helper()
	root := OverrideTree(t)
	if root == "" {
		root = repoRoot(t)
	}
	return filepath.Join(root, "durable", "interop") + string(filepath.Separator)
}

// SkipAtPin ends a test of Pi work the pin's Pi does not have: at the pin it skips with reason, and in a run that names another
// Pi (OverrideTreeEnv) it fails, because that run exists to answer the test and a Pi without the work would let it pass by
// skipping.
func SkipAtPin(t testing.TB, reason string) {
	t.Helper()
	if tree := OverrideTree(t); tree != "" {
		t.Fatalf("pioracle: the Pi that %s names (%s) lacks the work this test compares with: %s", OverrideTreeEnv, tree, reason)
	}
	t.Skip(reason)
}
