package subprocess

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A restarted extension's replica of the host's state is whole once the restart is adopted. The ready frame of the restart carries the new flags beside the
// catalog of the generation that ran before, because the catalog is the live registry, which the adoption replaces after the frame; nothing but a state
// push brings the replica (the Go, Rust, Python and Node SDKs' synchronous getCommands) up to date, and without one the replica stays stale until the
// extension's next dispatch. Only an isolated or fused extension restarts in place: a packed cell's member is started again as a new managed extension, so
// its ready frame is built after the previous generation's registry is gone.
func TestRestartPushesTheStateOnceItIsAdopted(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	recordCrashNotices(t, h)
	bridge := newTestBridge(&mockUIContext{})
	// The host's catalog is what the loaded extensions registered, as the runner reports it.
	bridge.SetHostAction("getCommands", func() []CommandInfo {
		var catalog []CommandInfo
		for _, ext := range h.Extensions() {
			if command, ok := ext.Command("probe"); ok {
				catalog = append(catalog, CommandInfo{Name: "probe", Description: command.Description, Source: "extension"})
			}
		}
		return catalog
	})
	h.SetUIBridge(bridge)
	// replica is the description of the probe command in the last state the extension received.
	var replica atomic.Value
	replica.Store("")
	serve, crash := generationServe("fused", func(state *StatePayload) {
		var catalog []CommandInfo
		if json.Unmarshal(state.Commands, &catalog) == nil {
			for _, command := range catalog {
				if command.Name == "probe" {
					replica.Store(command.Description)
				}
			}
		}
	})
	cfg := ExtConfig{Name: "fused", Enabled: true, SupervisorConfig: restartSupervisor}
	if _, err := h.LoadInProcess(t.Context(), cfg, serve); err != nil {
		t.Fatal(err)
	}
	rig := newRestartRig(t, h, "fused", func(*extension.Extension) func(context.Context) { return func(context.Context) { crash() } })
	rig.crash(t.Context())
	waitDeclarationGeneration(t, h, "fused", rig.held, "gen 2")
	deadline := time.Now().Add(testbudget.Wait(t))
	for replica.Load() != "gen 2" {
		if time.Now().After(deadline) {
			t.Fatalf("after the restart the extension's replica holds the probe command as %q, the host's catalog holds \"gen 2\"", replica.Load())
		}
		time.Sleep(time.Millisecond)
	}
}
