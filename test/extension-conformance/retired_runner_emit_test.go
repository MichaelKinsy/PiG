package extensionconformance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Pi's emit methods never look at staleness (runner.ts:1135-1560), so a session whose reload failed keeps delivering its events to the instance it invalidated
// (agent-session.ts:3825-3826, :3844); after dispose() (:1477-1496) and after a reload that loaded no Pi path emits to the old runner. Pig keeps a refusal for those late
// emits (Runner.assertEmitOpen), and a runner a failed reload left stale is retired by Close or by the reload that follows. The runner is the same over a process an
// extension has to itself (isolated) and over a process it shares with its neighbours (packed): one row per state and mode.
func TestConformance_RetiredRunnerRefusesEmitsAndAStaleOneDelivers(t *testing.T) {
	requireConformanceTool(t, "node")
	states := []struct {
		name      string
		apply     func(r *inproc.Runner, host *subprocess.Host)
		delivered bool
	}{
		{"failed reload", func(r *inproc.Runner, host *subprocess.Host) {
			r.InvalidateForReload("")
			host.Invalidate(r.StaleMessage())
		}, true},
		{"closed", func(r *inproc.Runner, _ *subprocess.Host) { r.Invalidate("runtime closed") }, false},
		{"superseded by a reload that loaded", func(r *inproc.Runner, _ *subprocess.Host) { r.InvalidateKeepingRuntime("") }, false},
		{"failed reload, then Close", func(r *inproc.Runner, host *subprocess.Host) {
			r.InvalidateForReload("")
			host.Invalidate(r.StaleMessage())
			r.Invalidate("runtime closed")
		}, false},
		{"failed reload, then a reload that loaded", func(r *inproc.Runner, host *subprocess.Host) {
			r.InvalidateForReload("")
			host.Invalidate(r.StaleMessage())
			r.InvalidateKeepingRuntime("")
		}, false},
	}
	for _, packed := range []bool{false, true} {
		mode := "isolated"
		if packed {
			mode = "packed"
		}
		for _, state := range states {
			t.Run(mode+"/"+state.name, func(t *testing.T) {
				dir := t.TempDir()
				log := filepath.Join(dir, "events.log")
				write := func(name string) string {
					path := filepath.Join(dir, name+".mjs")
					code := fmt.Sprintf(`import { appendFileSync } from "node:fs";
export default function (pi) {
  pi.on("tool_call", async (event) => { appendFileSync(%q, %q + " tool_call " + event.toolName + "\n"); });
  pi.on("input", async (event) => { appendFileSync(%q, %q + " input " + event.text + "\n"); });
}
`, log, name, log, name)
					if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
						t.Fatal(err)
					}
					return path
				}
				configs := []subprocess.ExtConfig{{Name: "first", Source: write("first"), Enabled: true, Isolation: "strict"}}
				if packed {
					configs = []subprocess.ExtConfig{{Name: "first", Source: write("first"), Enabled: true}, {Name: "second", Source: write("second"), Enabled: true}}
				}
				notify, status, actions := &[]string{}, &[]string{}, &[]string{}
				ui := newRecordingUI(notify, status)
				bridge := subprocess.NewUIBridge(func() {})
				bridge.SetUIContext(ui)
				bridge.SetActions(conformanceActions(actions))
				host := subprocess.NewHost(t.TempDir())
				host.SetHostInfo(conformanceHostInfo)
				host.SetUIBridge(bridge)
				host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return slices.Clone(configs), nil })
				t.Cleanup(func() { host.Shutdown("test done") })
				loaded, err := host.Reload(context.Background())
				if err != nil || len(loaded) != len(configs) {
					t.Fatalf("load: %d extensions, %v (%+v)", len(loaded), err, host.LastReloadReport())
				}
				runner := inproc.NewRunner(loaded, t.TempDir())
				state.apply(runner, host)

				_, toolErr := runner.EmitToolCall(context.Background(), extension.CustomToolCallEvent{ToolName: "read", Input: map[string]any{}})
				_, inputErr := runner.EmitInput(context.Background(), "hello", nil, extension.InputSourceUser, "")
				data, _ := os.ReadFile(log)
				lines := 0
				for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
					if line != "" {
						lines++
					}
				}
				want := 0
				if state.delivered {
					want = 2 * len(configs)
					if toolErr != nil || inputErr != nil {
						t.Fatalf("a stale runner that is still in service refused an emit: tool_call %v, input %v", toolErr, inputErr)
					}
				} else if !errors.Is(toolErr, extension.ErrStaleContext) || !errors.Is(inputErr, extension.ErrStaleContext) {
					t.Fatalf("a retired runner's emits = tool_call %v, input %v, want the stale-context error", toolErr, inputErr)
				}
				if lines != want {
					t.Fatalf("the extensions logged %d events, want %d:\n%s", lines, want, data)
				}
			})
		}
	}
}

// The same five states over every SDK's fixture, isolated and packed. The retired check is the in-process Runner's, before any call reaches an extension, so it does not depend on the
// SDK; what depends on it is that a runner a failed reload left stale still gets its event answered by the extension behind the host the interactive reload invalidated. The fixture's
// cache_warming_decision handler answers stop, so a delivery is an answer and a refusal is the stale error.
func TestConformance_RetiredRunnerRefusesAndAStaleOneDeliversInEverySDK(t *testing.T) {
	states := []struct {
		name      string
		apply     func(r *inproc.Runner, host *subprocess.Host)
		delivered bool
	}{
		{"failed reload", func(r *inproc.Runner, host *subprocess.Host) {
			r.InvalidateForReload("")
			if host != nil {
				host.Invalidate(r.StaleMessage())
			}
		}, true},
		{"closed", func(r *inproc.Runner, _ *subprocess.Host) { r.Invalidate("runtime closed") }, false},
		{"superseded by a reload that loaded", func(r *inproc.Runner, _ *subprocess.Host) { r.InvalidateKeepingRuntime("") }, false},
		{"failed reload, then Close", func(r *inproc.Runner, host *subprocess.Host) {
			r.InvalidateForReload("")
			if host != nil {
				host.Invalidate(r.StaleMessage())
			}
			r.Invalidate("runtime closed")
		}, false},
		{"failed reload, then a reload that loaded", func(r *inproc.Runner, host *subprocess.Host) {
			r.InvalidateForReload("")
			if host != nil {
				host.Invalidate(r.StaleMessage())
			}
			r.InvalidateKeepingRuntime("")
		}, false},
	}
	cases := append(allHarnessCases(), packedSDKHarnessCases()...)
	for _, tc := range cases {
		for _, state := range states {
			t.Run(tc.name+"/"+state.name, func(t *testing.T) {
				h := tc.make(t)
				t.Cleanup(func() {
					if h.cleanup != nil {
						h.cleanup()
					}
					if h.host != nil {
						h.host.Shutdown("test done")
					}
				})
				state.apply(h.runner, h.host)
				event := extension.CacheWarmingDecisionEvent{Type: "cache_warming_decision", WarmCost: 0.05, MissCost: 0.5, ContinuationProbability: 0.15, Action: extension.CacheWarmingActionWarm}
				action, err := h.runner.EmitCacheWarmingDecision(t.Context(), event)
				if state.delivered {
					if err != nil || action != extension.CacheWarmingActionStop {
						t.Fatalf("a stale runner still in service: action %q, error %v, want the extension's stop", action, err)
					}
					return
				}
				if !errors.Is(err, extension.ErrStaleContext) {
					t.Fatalf("a retired runner's emit = action %q, error %v, want the stale-context error", action, err)
				}
			})
		}
	}
}
