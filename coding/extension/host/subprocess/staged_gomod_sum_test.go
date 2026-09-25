package subprocess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStagedGoModFileSeedsGoSum pins the fix for source extensions with
// third-party dependencies failing to build through the staged-SDK path. Go
// resolves the checksum file next to the -modfile, so stagedGoModFile must
// seed <temp>.sum with the extension's go.sum or the build reports
// "missing go.sum entry" for every imported module.
func TestStagedGoModFileSeedsGoSum(t *testing.T) {
	srcDir := t.TempDir()
	const goMod = "module example.com/ext\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => ../gone/sdk\n"
	const goSum = "github.com/mattn/go-runewidth v0.0.23 h1:deadbeef=\ngithub.com/mattn/go-runewidth v0.0.23/go.mod h1:cafebabe=\n"
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "go.sum"), []byte(goSum), 0o644); err != nil {
		t.Fatal(err)
	}

	modPath, cleanup, err := stagedGoModFile(srcDir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	sumPath := strings.TrimSuffix(modPath, ".mod") + ".sum"
	got, err := os.ReadFile(sumPath)
	if err != nil {
		t.Fatalf("temp go.sum not seeded next to -modfile: %v", err)
	}
	if string(got) != goSum {
		t.Fatalf("seeded go.sum mismatch:\n got: %q\nwant: %q", got, goSum)
	}

	// cleanup must remove both temp files.
	cleanup()
	if _, err := os.Stat(sumPath); !os.IsNotExist(err) {
		t.Fatalf("cleanup left temp go.sum behind: %v", err)
	}
}

// TestStagedGoModFileNoGoSum confirms an extension without third-party deps
// (no go.sum) still stages successfully.
func TestStagedGoModFileNoGoSum(t *testing.T) {
	srcDir := t.TempDir()
	const goMod = "module example.com/ext\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => ../gone/sdk\n"
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	modPath, cleanup, err := stagedGoModFile(srcDir, t.TempDir())
	if err != nil {
		t.Fatalf("stage without go.sum failed: %v", err)
	}
	defer cleanup()
	if _, err := os.Stat(strings.TrimSuffix(modPath, ".mod") + ".sum"); !os.IsNotExist(err) {
		t.Fatalf("unexpected temp go.sum created when extension has none: %v", err)
	}
}
