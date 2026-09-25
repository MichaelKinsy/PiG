package ciimages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParityTargetsInstallPinnedComparator(t *testing.T) {
	makefile := readMakeSources(t, repoRoot(t))
	for _, required := range []string{
		"PI_PACKAGE_ROOT := $(CURDIR)/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent",
		"PIG_PARITY_PI_BIN := $(CURDIR)/extensions/sdk-ts/node_modules/.bin/pi",
		"CARGO_TARGET_DIR ?= $(CURDIR)/tmp/test-fixtures/rust-target",
		"parity-deps: interface-deps ## Install the exact locked Pi comparator and the TypeScript compiler its scenarios import\n\t@python3 automation/ci/npm-locked.py extensions/sdk-ts",
		"upstream-mirror: parity-deps",
		"test-sdk-ts: parity-deps",
		"parity-bin: parity-deps",
		"interface-inventory-drift: parity-deps interface-deps",
	} {
		if !strings.Contains(makefile, required) {
			t.Errorf("Makefile is missing %q", required)
		}
	}
}

func TestFixtureBuildsShareTheCargoTarget(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "automation", "ci", "test-fixtures.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`RUST_TARGET=${CARGO_TARGET_DIR:-"$OUT_DIR/rust-target"}`,
		`RUST_TARGET_EXPORT="export CARGO_TARGET_DIR=\"$RUST_TARGET\""`,
	} {
		if !strings.Contains(string(data), required) {
			t.Errorf("test fixture build is missing %q", required)
		}
	}
}

func TestMakeTestInstallsLockedInterfaceDependencies(t *testing.T) {
	makefile := readMakeSources(t, repoRoot(t))
	if !strings.Contains(makefile, "test: test-prereqs interface-deps parity-deps") {
		t.Fatal("make test does not depend on locked interface tooling")
	}
	if !strings.Contains(makefile, "interface-deps: ## Install the locked TypeScript compiler used by parity inventories\n\t@python3 automation/ci/npm-locked.py parity/interface-extractor") {
		t.Fatal("interface-deps does not use the content-checked locked installer")
	}
}

// readMakeSources returns the root Makefile followed by the fragments it
// includes from automation/make, so assertions hold wherever a rule lives.
func readMakeSources(t *testing.T, root string) string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "automation", "make", "*.mk"))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, path := range append([]string{filepath.Join(root, "Makefile")}, paths...) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
		b.WriteString("\n")
	}
	return b.String()
}
