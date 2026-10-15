package subprocess

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// goroutineGrace bounds how long a goroutine may take to return after its Host shut down.
const goroutineGrace = 10 * time.Second

// Every goroutine a Host starts for an extension ends when Shutdown returns: the connection's reader and writer, the
// process waiter, the supervisor, the stderr copy and the event dispatchers. A reload replaces an instance, and the old
// instance's goroutines must end with it, not at Shutdown. TestMain's leak check sees only goroutines blocked on a
// primitive nothing can reach; this gate also sees one that waits on a reachable socket, timer or process.
func TestHostLeavesNoGoroutineBehindAfterUseReloadAndShutdown(t *testing.T) {
	binPath := buildFixtureExt(t)
	baseline := testenv.BaselineGoroutines()
	configs := []ExtConfig{{Name: "fixture", Path: binPath, Enabled: true}}
	host := NewHost(t.TempDir())
	host.SetConfigLoader(func() ([]ExtConfig, error) { return append([]ExtConfig(nil), configs...), nil })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ext, err := host.Load(ctx, configs[0])
	if err != nil {
		t.Fatal(err)
	}
	result, err := ext.Tools["greet"].Definition.Execute(ctx, "tc-1", json.RawMessage(`{"name":"pig"}`), nil)
	if err != nil || result.Text() != "Hello, pig!" {
		t.Fatalf("greet = %q, %v", result.Text(), err)
	}
	if err := ext.Commands["hello"].Handler(ctx, "args"); err != nil {
		t.Fatal(err)
	}
	if _, err := ext.Handlers["session_start"][0](map[string]any{"reason": "startup"}); err != nil {
		t.Fatal(err)
	}

	// The first instance's goroutines, once its start-up work has settled, are the most a replacement instance may leave alive.
	oneInstance := len(baseline.Outliving())
	reloaded, err := host.Reload(ctx)
	if err != nil || len(reloaded) != 1 {
		t.Fatalf("Reload = %d extensions, %v", len(reloaded), err)
	}
	result, err = reloaded[0].Tools["greet"].Definition.Execute(ctx, "tc-2", json.RawMessage(`{"name":"again"}`), nil)
	if err != nil || result.Text() != "Hello, again!" {
		t.Fatalf("greet after reload = %q, %v", result.Text(), err)
	}

	baseline.RequireAtMost(t, oneInstance, goroutineGrace)

	host.Shutdown("goroutine gate")
	baseline.RequireEnded(t, goroutineGrace)
}

// A packed cell shares one process between its members. The host's goroutines for the cell (the process watcher, one
// connection per member, the log capture) end with Shutdown, and no member leaves one behind.
func TestHostLeavesNoGoroutineBehindAfterAPackedGoCellShutsDown(t *testing.T) {
	rootA := writePackedFactoryModule(t, "example.com/goroutinegate/a", "gate-a", "tool_a")
	rootB := writePackedFactoryModule(t, "example.com/goroutinegate/b", "gate-b", "tool_b")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cell, err := runtimecell.BuildGoPackedCell(ctx, t.TempDir(), "cell:goroutine-gate", []runtimecell.GoExtension{
		{Name: "gate-a", Root: rootA, ModulePath: "example.com/goroutinegate/a", Package: "example.com/goroutinegate/a", Factory: "Extension", Hash: "ha"},
		{Name: "gate-b", Root: rootB, ModulePath: "example.com/goroutinegate/b", Package: "example.com/goroutinegate/b", Factory: "Extension", Hash: "hb"},
	})
	if err != nil {
		t.Fatal(err)
	}
	baseline := testenv.BaselineGoroutines()
	host := NewHost(t.TempDir())
	exts, err := host.LoadGoPackedCell(ctx, cell)
	if err != nil || len(exts) != 2 {
		t.Fatalf("LoadGoPackedCell = %d extensions, %v", len(exts), err)
	}
	for _, ext := range exts {
		for name, tool := range ext.Tools {
			if _, err := tool.Definition.Execute(ctx, "tc-"+name, json.RawMessage(`{}`), nil); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
	}
	host.Shutdown("goroutine gate")
	baseline.RequireEnded(t, goroutineGrace)
}
