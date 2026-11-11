package runtimecell

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The warm cell is rendered by the code a real cell is: its manifest is a cell's with no extensions, so the pig-sdk unit
// it builds is the one cells reuse, and it carries the SDK's lock like a cell.
func TestWriteRustWarmCellRendersTheManifestAndLockOfACell(t *testing.T) {
	sdk := t.TempDir()
	lock := []byte("# lock\nversion = 4\n")
	if err := os.WriteFile(filepath.Join(sdk, "Cargo.lock"), lock, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "cell")
	if err := WriteRustWarmCell(dir, sdk); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if want := renderRustCargoToml(nil, sdk, "pig-warm-cell"); string(manifest) != want {
		t.Fatalf("Cargo.toml = %q, want the cell rendering %q", manifest, want)
	}
	got, err := os.ReadFile(filepath.Join(dir, "Cargo.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, lock) {
		t.Fatalf("Cargo.lock = %q, want the SDK's %q", got, lock)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "main.rs")); err != nil {
		t.Fatalf("warm cell has no main.rs: %v", err)
	}
}
