package subprocess

import (
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofrs/flock"
)

// Shutdown drains every packed process the host started, including one a
// Reload replaced: Reload stops the old process without waiting for it. A
// cache usage lease still held after Shutdown keeps the cache in use, and on
// Windows the open lock file cannot be deleted.
func TestShutdownReleasesReplacedPackedProcessLeases(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "ctx-mode.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	pigHome := t.TempDir()
	t.Setenv("PIG_HOME", pigHome)
	h := NewHost(t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) {
		return []ExtConfig{{Name: "ctx-mode", Source: fixture, Enabled: true}}, nil
	})
	for range 2 {
		if loaded, err := h.Reload(t.Context()); err != nil || len(loaded) != 1 {
			t.Fatalf("reload = %d extensions, %v", len(loaded), err)
		}
	}
	h.Shutdown("test done")

	var locks []string
	err = filepath.WalkDir(pigHome, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".usage.lock") {
			locks = append(locks, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) == 0 {
		t.Fatal("the packed cell took no cache usage lease")
	}
	for _, path := range locks {
		lock := flock.New(path)
		locked, err := lock.TryLock()
		if err != nil || !locked {
			t.Errorf("usage lease %s still held after Shutdown (TryLock = %v, %v)", path, locked, err)
			continue
		}
		_ = lock.Unlock()
	}
}
