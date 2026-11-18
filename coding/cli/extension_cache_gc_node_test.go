//go:build !pig_strip_node_extensions

package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Loading extensions, the startup path, only schedules the collection: nothing collects until a mode
// starts it, so a load never waits for the cache.
func TestLoadingExtensionsSchedulesButDoesNotRunTheCacheCollection(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	cacheRoot := isolateAutomaticCacheGC(t)
	var runs, prunes atomic.Int32
	automaticCacheGCRun = func(context.Context, []subprocess.ExtConfig) error { runs.Add(1); return nil }
	// Counting prunes too catches a load that calls the collector directly rather than through the scheduled run.
	automaticCacheGCPrune = func(runtimecell.CacheLifecycleOptions) (runtimecell.CacheReport, error) {
		prunes.Add(1)
		return runtimecell.CacheReport{}, nil
	}
	fixture, err := filepath.Abs(filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "testdata", "ctx-mode.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	configs := []subprocess.ExtConfig{{Name: "ctx-mode", Source: fixture, Enabled: true}}
	_, host, _, errs := loadSubprocessExtensions(t.Context(), t.TempDir(), extension.ModePrint, nil, configs, nil, nil)
	if len(errs) != 0 {
		t.Fatalf("load errors = %v", errs)
	}
	defer host.Shutdown("test done")
	if runs.Load() != 0 || prunes.Load() != 0 {
		t.Fatalf("loading extensions ran the cache collection: runs=%d prunes=%d", runs.Load(), prunes.Load())
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, ".last-auto-gc")); !os.IsNotExist(err) {
		t.Fatalf("loading extensions wrote the collection marker: %v", err)
	}
	startAutomaticExtensionCacheGC(t.Context())
	stopAutomaticExtensionCacheGC()
	if runs.Load() != 1 {
		t.Fatalf("started collections = %d, want 1 after the load scheduled one", runs.Load())
	}
}
