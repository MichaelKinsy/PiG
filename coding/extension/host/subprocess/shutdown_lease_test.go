//go:build !pig_strip_node_extensions

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
	assertUsageLeasesReleased(t, pigHome, "after Shutdown")
}

// A packed process that ends on its own releases its cache usage lease even
// when no stop dropped the reference a state holds on it. The connection-close
// handler disables each member of a dead process without stopping the shared
// state, so when every member's handler runs before the process watcher, no
// member is left for the watcher's quarantine to stop.
func TestPackedProcessExitReleasesUsageLeaseAfterMembersDisabled(t *testing.T) {
	rootA := writePackedFactoryModule(t, "example.com/exitlease/a", "exit-lease-a", "tool_a")
	rootB := writePackedFactoryModule(t, "example.com/exitlease/b", "exit-lease-b", "tool_b")
	configs := []ExtConfig{
		packedFactoryConfig("exit-lease-a", rootA, "example.com/exitlease/a", "ha"),
		packedFactoryConfig("exit-lease-b", rootB, "example.com/exitlease/b", "hb"),
	}
	configRoot := t.TempDir()
	h := NewHostWithConfigRoot(t.TempDir(), configRoot)
	t.Cleanup(func() { h.Shutdown("test done") })
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	if loaded, err := h.Reload(t.Context()); err != nil || len(loaded) != len(configs) {
		t.Fatalf("reload = %d extensions, %v", len(loaded), err)
	}
	h.mu.Lock()
	members := []*managedExt{h.exts["exit-lease-a"], h.exts["exit-lease-b"]}
	h.mu.Unlock()
	process := members[0].packedProcess
	if process == nil || members[1] == nil || members[1].packedProcess != process {
		t.Fatalf("expected both members in one packed process, got %#v", members)
	}
	for _, me := range members {
		h.disablePackedMember(me, "packed member connection closed")
	}
	if err := process.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-process.watcherDone
	if !process.share.alive() {
		t.Fatal("a stop released the process reference; the regression needs the reference held")
	}
	assertUsageLeasesReleased(t, configRoot, "after the packed process exited")
}

// assertUsageLeasesReleased requires at least one cache usage lock under root and that none of them is held.
func assertUsageLeasesReleased(t *testing.T, root, when string) {
	t.Helper()
	var locks []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
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
			t.Errorf("usage lease %s still held %s (TryLock = %v, %v)", path, when, locked, err)
			continue
		}
		_ = lock.Unlock()
	}
}
