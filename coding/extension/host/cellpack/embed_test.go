package cellpack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// extract must land each embedded binary at dest/<Binary>, executable, and the
// resolver built from the same manifest must then hit it. This exercises the
// whole embed→extract→resolve pipeline without a real build.
func TestExtractThenResolve(t *testing.T) {
	m := Manifest{PigCoreVersion: "test", Cells: []CellEntry{{
		Language: "go", Key: "go-core", OS: runtime.GOOS, Arch: runtime.GOARCH,
		Binary:     "go/aaaa/runner",
		Extensions: []ExtEntry{{Name: "context-info", Hash: "h1"}, {Name: "subagent", Hash: "h2"}},
	}}}
	fsys := fstest.MapFS{
		"cells/manifest.json":  {Data: mustJSON(t, m)},
		"cells/go/aaaa/runner": {Data: []byte("BINARY")},
	}

	if got, err := loadManifest(fsys); err != nil || len(got.Cells) != 1 {
		t.Fatalf("loadManifest = %+v, %v", got, err)
	}

	dest := t.TempDir()
	if err := extract(fsys, m, dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	// Windows starts only files with an executable extension; elsewhere the
	// execute bit makes the binary runnable.
	want := extractedAt(dest, "go/aaaa/runner")
	fi, err := os.Stat(want)
	if err != nil {
		t.Fatalf("extracted binary missing: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode()&0o100 == 0 {
		t.Errorf("extracted binary not executable: mode %v", fi.Mode())
	}

	r := NewResolver(m, dest)
	got, ok := r.ResolvePrebuilt(runtimecell.PrebuiltRequest{
		Language: "go", Key: "go-core", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Extensions: []runtimecell.PrebuiltExtension{{Name: "context-info", Hash: "h1"}, {Name: "subagent", Hash: "h2"}},
	})
	if !ok || got != want {
		t.Errorf("resolve = %q, %v; want extracted path", got, ok)
	}
}

func TestEmbeddedDefaultManifestIsEmpty(t *testing.T) {
	m, err := loadManifest(cells)
	if err != nil {
		t.Fatalf("embedded default manifest must parse: %v", err)
	}
	if len(m.Cells) != 0 {
		t.Errorf("committed default manifest must be empty, got %d cells", len(m.Cells))
	}
}

func TestLoadManifestRejectsRetiredStaticAuthenticationMetadata(t *testing.T) {
	retiredField := "auth" + "Targets"
	data := []byte(`{"pigCoreVersion":"","cells":[],"` + retiredField + `":[]}`)
	_, err := loadManifest(fstest.MapFS{"cells/manifest.json": {Data: data}})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("loadManifest error = %v, want unknown retired field", err)
	}
}

// Regression: the extraction directory followed <home>/.pig whatever PIG_HOME said, so a Termux or relocated PiG home held the cells outside its own root.
func TestExtractDirFollowsPigHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "relocated")
	t.Setenv("PIG_HOME", home)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "elsewhere"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	dir, err := extractDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "piglet-binary-cells"); dir != want {
		t.Fatalf("extractDir() = %q, want %q", dir, want)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("extractDir() did not create %q: %v", dir, err)
	}
}

// The host starts a cell with cmd.Dir set to the session directory, so a relative PIG_HOME must not leave a cell path that resolves against it.
func TestExtractDirIsAbsoluteForARelativePigHome(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("PIG_HOME", "relative-home")
	dir, err := extractDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cwd, "relative-home", "piglet-binary-cells"); dir != want {
		t.Fatalf("extractDir() = %q, want %q", dir, want)
	}
}
