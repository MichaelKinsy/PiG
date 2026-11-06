package noderealpath

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// fs.realpathSync resolves the path before it walks it, so a ".." step cancels a component that does not exist, and a relative path comes back absolute. filepath.EvalSymlinks looks the component up first and fails on Windows.
func TestSyncResolvesDotSegmentsBeforeWalking(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Mkdir(filepath.Join(dir, "r2"), 0o755); err != nil {
		t.Fatal(err)
	}
	want, err := Sync(filepath.Join(dir, "r2"))
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"./rel/../r2", "rel/../r2", filepath.Join(dir, "rel", "..", "r2")} {
		if got, err := Sync(input); err != nil || got != want {
			t.Errorf("Sync(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if got, err := Sync("rel"); err == nil {
		t.Errorf("Sync(missing) = %q, nil; want the error Node throws", got)
	}
}

func TestSyncFollowsADirectoryLink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	testenv.RequireDirectoryLink(t, real, link)
	want, err := Sync(real)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Sync(link); err != nil || got != want {
		t.Errorf("Sync(link) = %q, %v; want %q", got, err, want)
	}
}
