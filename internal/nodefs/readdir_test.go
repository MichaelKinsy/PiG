package nodefs

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// Node's fs.readdirSync lists entries as libuv's scandir returns them: sorted by name with strcmp on Unix, and in the file system's order
// on Windows, which on NTFS ignores case (probed natively with Node 24 and Pi 1.1.0: aprompt, Mprompt, Zprompt).
func TestReadDirKeepsNodesOrder(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Zeta", "alpha", "Beta", "a.md", "B.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	want := []string{"B.md", "Beta", "Zeta", "a.md", "alpha"}
	if runtime.GOOS == "windows" {
		want = []string{"a.md", "alpha", "B.md", "Beta", "Zeta"}
	}
	if !slices.Equal(names, want) {
		t.Fatalf("ReadDir = %v, want %v", names, want)
	}
}

func TestReadDirReportsAMissingDirectory(t *testing.T) {
	if _, err := ReadDir(filepath.Join(t.TempDir(), "missing")); !os.IsNotExist(err) {
		t.Fatalf("ReadDir(missing) error = %v, want not-exist", err)
	}
}
