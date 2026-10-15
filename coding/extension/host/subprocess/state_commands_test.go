package subprocess

import (
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The command catalog is encoded once per version the host reports and again whenever the version moves, and the bytes equal a direct encoding. A host that reports no version encodes it on every push.
func TestSnapshotEncodesTheCommandCatalogByVersion(t *testing.T) {
	var version atomic.Uint64
	var builds atomic.Int64
	commands := []CommandInfo{{Name: "alpha", Description: "first"}}
	bridge := NewUIBridge(func() {})
	bridge.SetHostAction("getCommands", func() []CommandInfo { builds.Add(1); return commands })
	bridge.SetHostAction("getCommandsVersion", version.Load)
	version.Store(1)
	check := func(step string) {
		t.Helper()
		want, err := json.Marshal(commands)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(bridge.Snapshot(nil, 0, false).Commands); got != string(want) {
			t.Fatalf("%s:\n got %s\nwant %s", step, got, want)
		}
	}
	for range 3 {
		check("version 1")
	}
	if builds.Load() != 1 {
		t.Fatalf("the catalog was built %d times at one version, want 1", builds.Load())
	}
	commands = []CommandInfo{{Name: "alpha", Description: "first"}, {Name: "beta"}}
	version.Store(2)
	check("version 2")
	check("version 2 again")
	if builds.Load() != 2 {
		t.Fatalf("the catalog was built %d times over two versions, want 2", builds.Load())
	}
	// Rebinding the callbacks drops the encoding even when the new ones report a version already served.
	commands = []CommandInfo{{Name: "gamma"}}
	bridge.SetHostAction("getCommands", func() []CommandInfo { builds.Add(1); return commands })
	check("rebound")

	// No version: every push encodes the catalog.
	unversioned := NewUIBridge(func() {})
	var plainBuilds atomic.Int64
	unversioned.SetHostAction("getCommands", func() []CommandInfo { plainBuilds.Add(1); return commands })
	for range 3 {
		unversioned.Snapshot(nil, 0, false)
	}
	if plainBuilds.Load() != 3 {
		t.Fatalf("a host without a version built the catalog %d times over 3 pushes, want 3", plainBuilds.Load())
	}
}
