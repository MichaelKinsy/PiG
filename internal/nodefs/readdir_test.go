package nodefs

import (
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
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

// Node decodes each entry name as UTF-8 (U+FFFD per ill-formed subpart), so a path joined from it does not name the file; the entry itself stays usable.
func TestReadDirDecodesNamesLikeNode(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("file names are arbitrary bytes only on Linux")
	}
	dir := t.TempDir()
	for _, name := range []string{"ok", "a\xffb", "t\xe2\x82"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Skipf("file system refuses the name %q: %v", name, err)
		}
	}
	entries, err := ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
		if !entry.Type().IsRegular() {
			t.Errorf("%q lost its type", entry.Name())
		}
	}
	if want := []string{"a\uFFFDb", "ok", "t\uFFFD"}; !slices.Equal(names, want) {
		t.Fatalf("names = %q, want %q", names, want)
	}
}

// Pi's collectors take an entry's type from the directory entry and stat only a symlink, so a decoded name that no longer names its file still counts as a file, and a broken link is skipped.
func TestEntryKindClassifiesLikePisCollectors(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("file names are arbitrary bytes only on Linux")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f\xff"), nil, 0o644); err != nil {
		t.Skip(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.RequireDirectoryLink(t, filepath.Join(dir, "d"), filepath.Join(dir, "link"))
	testenv.Symlink(t, filepath.Join(dir, "missing"), filepath.Join(dir, "gone"))
	entries, err := ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][3]bool{}
	for _, entry := range entries {
		isDir, isFile, ok := EntryKind(dir, entry)
		got[entry.Name()] = [3]bool{isDir, isFile, ok}
	}
	want := map[string][3]bool{"d": {true, false, true}, "f\uFFFD": {false, true, true}, "link": {true, false, true}, "gone": {false, false, false}}
	if !maps.Equal(got, want) {
		t.Fatalf("EntryKind = %v, want %v", got, want)
	}
}
