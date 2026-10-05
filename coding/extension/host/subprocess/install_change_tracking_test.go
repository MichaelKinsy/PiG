package subprocess

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/installchange"
)

// pig additive (D95): a process records the cell files it starts, so a later error can tell that another pig pruned them.

// trackedUnder returns the tracked files below root, as other tests in the binary may track their own.
func trackedUnder(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	for _, path := range installchange.Default().TrackedFiles() {
		if relative, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(relative, "..") {
			found = append(found, path)
		}
	}
	return found
}

func TestHostTracksTheNodeCellItStarts(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	installchange.Record()
	fixture, err := filepath.Abs(filepath.Join("testdata", "ctx-mode.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	h.SetConfigLoader(func() ([]ExtConfig, error) {
		return []ExtConfig{{Name: "ctx-mode", Source: fixture, Enabled: true}}, nil
	})
	if trackedUnder(t, configRoot) != nil {
		t.Fatal("a file was tracked before any extension started")
	}
	if loaded, err := h.Reload(t.Context()); err != nil || len(loaded) != 1 {
		t.Fatalf("reload = %d extensions, %v", len(loaded), err)
	}
	tracked := trackedUnder(t, configRoot)
	if len(tracked) == 0 {
		t.Fatal("the started node cell was not tracked")
	}
	for _, path := range tracked {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("tracked cell file: %v", err)
		}
	}
	// Another pig prunes the cache: the next check names the missing file. That pig could only prune cell files no running
	// cell holds open (Windows refuses to delete a file a live process has open), so stop this host's cell and wait for it to
	// release its cache usage lease first (Shutdown reaps every process before it returns).
	h.Shutdown("prune the cell cache")
	if err := os.RemoveAll(filepath.Join(configRoot, "cache")); err != nil {
		t.Fatal(err)
	}
	change := installchange.Detect()
	if change == nil || change.Kind != installchange.FilesPruned {
		t.Fatalf("a pruned cache reported %+v, want %q", change, installchange.FilesPruned)
	}
}

func TestHostTracksThePackedGoCellItStarts(t *testing.T) {
	installchange.Record()
	root := writePackedFactoryModule(t, "example.com/track/a", "track-a", "tool_a")
	configs := []ExtConfig{packedFactoryConfig("track-a", root, "example.com/track/a", "ha")}
	configRoot := t.TempDir()
	h := NewHostWithConfigRoot(t.TempDir(), configRoot)
	t.Cleanup(func() { h.Shutdown("test done") })
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	if loaded, err := h.Reload(t.Context()); err != nil || len(loaded) != 1 {
		t.Fatalf("reload = %d extensions, %v", len(loaded), err)
	}
	h.mu.Lock()
	process := h.exts["track-a"].packedProcess
	h.mu.Unlock()
	if process == nil {
		t.Fatal("the extension did not run in a packed process")
	}
	binary := process.cmd.Path
	if !slices.Contains(trackedUnder(t, configRoot), binary) {
		t.Fatalf("the packed cell binary %s is not tracked: %v", binary, trackedUnder(t, configRoot))
	}
}
