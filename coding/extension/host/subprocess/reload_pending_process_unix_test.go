//go:build unix && !pig_strip_node_extensions

package subprocess

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// TestRetiredGenerationsWithPendingCallsEndWithTheSession pins the lifetime of a generation a reload retired while its command awaits a ui.custom overlay: Pi's old
// instance lives on, and so does its process, until the command or the session ends. At most one process per reload that caught a command mid-surface stays (the shared
// cell's process, or the extension's own in isolated mode), and the session's end leaves none of them.
func TestRetiredGenerationsWithPendingCallsEndWithTheSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping packed SDK builds in short mode")
	}
	t.Parallel()
	for _, isolation := range []string{"shared-ok", "strict"} {
		t.Run(isolation, func(t *testing.T) {
			configs := focusedPackedSDKCases()[0].configs(t)
			for i := range configs {
				configs[i].Isolation = isolation
			}
			fakeUI := newTestUIContext()
			host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			bridge := NewUIBridge(func() {})
			bridge.SetUIContext(fakeUI)
			bridge.SetHostAction("streamModel", func(context.Context, map[string]any, map[string]any) (*ai.AssistantMessageEventStream, error) {
				return packedTestModelStream()
			})
			host.SetUIBridge(bridge)
			defer host.Shutdown("test done")
			ctx, cancel := buildContext(t)
			defer cancel()
			enabled := configs
			host.SetConfigLoader(func() ([]ExtConfig, error) { return enabled, nil })
			if loaded, errs := host.LoadAll(ctx, configs); len(errs) > 0 || len(loaded) != 2 {
				t.Fatalf("LoadAll loaded=%d errors=%v", len(loaded), errs)
			}
			waiters := &waiterHost{host: host}
			everyPid := map[int]bool{}
			// A reload that replaces an extension keeps its process (Pi's loader re-invokes the factory in it), so each cycle removes the extension while its command awaits
			// the overlay and loads it again.
			const cycles = 2
			for range cycles {
				command := extensionNamed(host, configs[0].Name).Commands["focused"]
				go func() { _ = command.Handler(context.Background(), "") }()
				fakeUI.awaitOverlay(t, 5*time.Second)
				for pid := range waiters.processes() {
					everyPid[pid] = true
				}
				enabled = nil
				if _, err := host.Reload(ctx); err != nil {
					t.Fatal(err)
				}
				enabled = configs
				if _, err := host.Reload(ctx); err != nil {
					t.Fatal(err)
				}
			}
			// The last reload loaded the extensions again: the processes of the retired generations whose commands await their overlays stay besides.
			current := map[int]bool{}
			for pid := range waiters.processes() {
				current[pid] = true
			}
			held := 0
			for pid := range everyPid {
				if waiterProcessAlive(pid) {
					held++
				}
			}
			// At most one process per reload that caught a command mid-surface stays: loading the same extension again takes over the process a retired generation holds
			// instead of starting another (Pi's loader re-invokes the factory in it), so the same two cycles hold one.
			if held < 1 || held > cycles {
				t.Fatalf("%d of the %d processes of the retired generations are alive after %d reloads that caught a command mid-surface, want between 1 and one per reload (current generation: %d processes)", held, len(everyPid), cycles, len(current))
			}
			host.Shutdown("session ended")
			deadline := time.Now().Add(10 * time.Second)
			for {
				left := 0
				for pid := range everyPid {
					if waiterProcessAlive(pid) {
						left++
					}
				}
				if left == 0 {
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("%d extension processes outlived the session", left)
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
}
