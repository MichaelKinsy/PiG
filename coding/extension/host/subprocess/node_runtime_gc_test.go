package subprocess

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

func TestNodeRuntimeCacheParticipatesInGC(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(t.TempDir(), "runtime")
	if err := materializeNodeRuntime(t.Context(), filepath.Join(root, "ext"), destination); err != nil {
		t.Fatal(err)
	}
	options := runtimecell.CacheLifecycleOptions{CacheRoot: root, Now: func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }}
	report, err := runtimecell.InspectCache(options)
	if err != nil {
		t.Fatal(err)
	}
	// One materialization publishes one independently managed immutable artifact.
	if len(report.Entries) != 1 || report.Entries[0].Class != runtimecell.CacheInactiveExpired {
		t.Fatalf("runtime is not a managed cache entry: %+v", report.Entries)
	}
	lease, err := runtimecell.AcquireArtifactUsageLease(filepath.Join(report.Entries[0].Path, "content.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lease.Release(); err != nil {
			t.Error(err)
		}
	}()
	protected, err := runtimecell.PruneCaches(options)
	if err != nil {
		t.Fatal(err)
	}
	if protected.Removed != 0 || protected.Entries[0].Class != runtimecell.CacheActive {
		t.Fatalf("active source was pruned: %+v", protected)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	pruned, err := runtimecell.PruneCaches(options)
	if err != nil {
		t.Fatal(err)
	}
	if pruned.Removed != 1 {
		t.Fatalf("unused runtime was not pruned: %+v", pruned)
	}
	if _, err := os.ReadFile(filepath.Join(destination, "runtime.mjs")); err != nil {
		t.Fatalf("pruning shared runtime damaged the launcher: %v", err)
	}
}
