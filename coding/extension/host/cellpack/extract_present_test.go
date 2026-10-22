package cellpack

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"
)

func goCellFixture(t *testing.T, content string) (fstest.MapFS, Manifest) {
	t.Helper()
	m := Manifest{PigCoreVersion: "test", Cells: []CellEntry{{
		Language: "go", Key: "go-core", OS: runtime.GOOS, Arch: runtime.GOARCH,
		Binary:     "go/aaaa/runner",
		Extensions: []ExtEntry{{Name: "context-info", Hash: "h1"}},
	}}}
	return fstest.MapFS{
		"cells/manifest.json":  {Data: mustJSON(t, m)},
		"cells/go/aaaa/runner": {Data: []byte(content)},
	}, m
}

// A second Piglet Binary instance starts while the first executes the extracted cell. Windows refuses to replace the file of a running image, so extraction leaves a cell binary that already holds the embedded bytes untouched.
func TestExtractLeavesAnIdenticalCellBinaryInPlace(t *testing.T) {
	fsys, m := goCellFixture(t, "BINARY")
	dest := t.TempDir()
	if err := extract(fsys, m, dest); err != nil {
		t.Fatal(err)
	}
	out := extractedBinaryPath(dest, "go/aaaa/runner")
	before, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := extract(fsys, m, dest); err != nil {
		t.Fatalf("second extract: %v", err)
	}
	after, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("the second extract replaced the cell binary, which a running instance may be executing")
	}
}

// The cell binary cannot be replaced, as on Windows while an instance runs it: extraction still succeeds because the file already holds the embedded bytes.
func TestExtractSucceedsWhenTheIdenticalBinaryCannotBeReplaced(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory this user cannot write")
	}
	fsys, m := goCellFixture(t, "BINARY")
	dest := t.TempDir()
	if err := extract(fsys, m, dest); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(extractedBinaryPath(dest, "go/aaaa/runner"))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := extract(fsys, m, dest); err != nil {
		t.Fatalf("extract over an identical, unreplaceable binary: %v", err)
	}
}

// A binary with other bytes, or without the execute bit, is replaced.
func TestExtractReplacesADifferentCellBinary(t *testing.T) {
	fsys, m := goCellFixture(t, "NEW BINARY")
	dest := t.TempDir()
	out := extractedBinaryPath(dest, "go/aaaa/runner")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, stale := range map[string]string{"other bytes": "OLD BINARY", "prefix": "NEW", "longer": "NEW BINARY!"} {
		if err := os.WriteFile(out, []byte(stale), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := extract(fsys, m, dest); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, err := os.ReadFile(out); err != nil || string(got) != "NEW BINARY" {
			t.Fatalf("%s: binary = %q, %v", name, got, err)
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(out, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := extract(fsys, m, dest); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(out); err != nil || info.Mode().Perm()&0o100 == 0 {
			t.Fatalf("binary without the execute bit was kept: %v, %v", info, err)
		}
	}
}
