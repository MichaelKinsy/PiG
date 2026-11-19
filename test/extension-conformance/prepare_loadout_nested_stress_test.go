package extensionconformance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The Node runtime starts a tool_prepare_loadout request nested inside the synchronous call its extension waits on (#516). The rows here put that path under what an extension does inside it: a hook that registers another tool or calls a synchronous API itself, a hook that throws, and a reload or an abort that arrives while the hook runs and the call is still open. Each one finishes, or the load / prompt bound fails it.

const stressTools = `const schema = { type: "object", properties: {} };
const tool = (name, description, extra) => ({ name, label: name, description, parameters: schema, ...extra, async execute() { return { content: [{ type: "text", text: name }] }; } });
`

var nodeNestedStressFixtures = []struct {
	name, source string
	want         []string
	failures     []string
}{
	{"a hook that registers another tool", stressTools + `export default function (pi: any) {
  let made = false;
  pi.registerTool(tool("hooked", "Hooked.", { prepareLoadout() {
    if (!made) { made = true; pi.registerTool(tool("from_hook", "From hook.")); }
    return { descriptions: { hooked: "Hook ran" } };
  } }));
  pi.on("session_start", async () => { pi.registerTool(tool("late", "Late.")); });
}
`, []string{"from_hook: From hook.", "hooked: Hook ran", "late: Late."}, nil},
	{"a hook that calls synchronous APIs", stressTools + `export default function (pi: any) {
  pi.registerTool(tool("hooked", "Hooked.", { prepareLoadout() {
    const active = pi.getActiveTools();
    const all = pi.getAllTools();
    return { descriptions: { hooked: "Hook saw " + Array.isArray(active) + " " + (all.length > 0) } };
  } }));
  pi.on("session_start", async () => { pi.registerTool(tool("late", "Late.")); });
}
`, []string{"hooked: Hook saw true true", "late: Late."}, nil},
	{"a hook that throws", stressTools + `export default function (pi: any) {
  pi.registerTool(tool("hooked", "Hooked.", { prepareLoadout() { throw new Error("boom"); } }));
  pi.on("session_start", async () => { pi.registerTool(tool("late", "Late.")); });
}
`, []string{"hooked: Hooked.", "late: Late."}, []string{"prepare_loadout"}},
}

func TestNodeNestedPrepareLoadoutSurvivesWhatTheHookDoes(t *testing.T) {
	t.Parallel()
	for _, fixture := range nodeNestedStressFixtures {
		t.Run(fixture.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "extension.ts")
			if err := os.WriteFile(path, []byte(fixture.source), 0o600); err != nil {
				t.Fatal(err)
			}
			h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { h.Shutdown("test complete") })
			bridge := subprocess.NewUIBridge(func() {})
			h.SetUIBridge(bridge)
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			loaded, failures := h.LoadAll(ctx, []subprocess.ExtConfig{{Name: "stress", Source: path, Enabled: true, Isolation: "strict"}})
			if len(failures) != 0 || len(loaded) != 1 {
				t.Fatalf("load: %v", failures)
			}
			s := newSyncLoadoutSession(t, ctx, h, bridge, loaded)
			var declared []string
			s.faux.SetResponses([]ai.FauxResponseStep{
				ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
					for _, tool := range ai.GetCurrentTools(request.Messages()) {
						declared = append(declared, tool.Name+": "+tool.Description)
					}
					slices.Sort(declared)
					return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}.AssistantMessage(), nil
				}),
			})
			if err := s.session.Prompt(ctx, "go"); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(declared, fixture.want) {
				t.Errorf("declarations = %q, want %q", declared, fixture.want)
			}
			var events []string
			for _, report := range s.reported() {
				events = append(events, strings.SplitN(report, ":", 2)[0])
			}
			if !failureEventsAre(events, fixture.failures) {
				t.Errorf("reported failures = %q, want only events %q", s.reported(), fixture.failures)
			}
		})
	}
}

// failureEventsAre reports whether events holds at least one failure and only the wanted ones (the host asks a hook on every refresh, so a throwing hook is reported more than once), or none when nothing is wanted.
func failureEventsAre(events, want []string) bool {
	if len(want) == 0 {
		return len(events) == 0
	}
	if len(events) == 0 {
		return false
	}
	for _, event := range events {
		if !slices.Contains(want, event) {
			return false
		}
	}
	return true
}

// A tool call registers a tool while the hook it triggers is still running (a busy hook: Pi's hook is synchronous too), and a reload, or an abort, arrives in that window. The extension, the host and the session finish: the prompt and the reload return, and the extension serves the next call.
func TestNodeNestedPrepareLoadoutWithAReloadOrAnAbortWhileTheCallIsOpen(t *testing.T) {
	t.Parallel()
	for _, arrival := range []string{"reload", "abort"} {
		t.Run(arrival, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "hook-running")
			source := stressTools + fmt.Sprintf(`import * as fs from "node:fs";
export default function (pi: any) {
  pi.registerTool(tool("hooked", "Hooked.", { prepareLoadout() {
    fs.writeFileSync(%q, "running");
    const end = Date.now() + 2000;
    while (Date.now() < end) {}
    return null;
  } }));
  pi.registerTool({ ...tool("register_now", "Registers."), async execute() {
    pi.registerTool(tool("made_late", "Made late."));
    return { content: [{ type: "text", text: "registered" }] };
  } });
  pi.registerTool(tool("noop", "Noop."));
}
`, marker)
			path := filepath.Join(dir, "extension.ts")
			if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { h.Shutdown("test complete") })
			bridge := subprocess.NewUIBridge(func() {})
			h.SetUIBridge(bridge)
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
			defer cancel()
			configs := []subprocess.ExtConfig{{Name: "stress", Source: path, Enabled: true, Isolation: "strict"}}
			loaded, failures := h.LoadAll(ctx, configs)
			if len(failures) != 0 || len(loaded) != 1 {
				t.Fatalf("load: %v", failures)
			}
			h.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
			s := newSyncLoadoutSession(t, ctx, h, bridge, loaded)
			_ = os.Remove(marker)
			s.faux.SetResponses([]ai.FauxResponseStep{
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("register_now", map[string]any{}, &ai.FauxToolCallOptions{ID: "call-1"})}, StopReason: "toolUse"}),
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
			})
			var wg sync.WaitGroup
			var reloaded []extension.Extension
			var reloadErr error
			wg.Go(func() {
				deadline := time.Now().Add(60 * time.Second)
				for time.Now().Before(deadline) {
					if _, err := os.Stat(marker); err == nil {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if arrival == "reload" {
					reloaded, reloadErr = h.Reload(ctx)
				} else {
					reloadErr = s.session.Abort(ctx)
				}
			})
			prompted := make(chan error, 1)
			go func() { prompted <- s.session.Prompt(ctx, "go") }()
			select {
			case <-prompted:
			case <-time.After(2 * time.Minute):
				t.Fatalf("the prompt did not return after a %s while the hook ran", arrival)
			}
			wg.Wait()
			if reloadErr != nil {
				t.Fatalf("%s: %v", arrival, reloadErr)
			}
			if arrival == "reload" {
				if len(reloaded) != 1 {
					t.Fatalf("reload loaded %d extensions", len(reloaded))
				}
				// The reloaded extension answers a fresh session's first turn.
				if declared := syncLoadoutDeclarations(t, ctx, h, bridge, reloaded); !slices.Contains(declared, "noop: Noop.") {
					t.Fatalf("declarations after reload = %q", declared)
				}
				return
			}
			// After the abort the same extension serves the next call.
			var results []string
			s.faux.SetResponses([]ai.FauxResponseStep{
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("noop", map[string]any{}, &ai.FauxToolCallOptions{ID: "call-2"})}, StopReason: "toolUse"}),
				ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
					for _, message := range request.Messages() {
						if result, ok := message.(ai.ToolResultMessage); ok && result.ToolName == "noop" {
							for _, block := range result.Content {
								if text, ok := block.(ai.TextContent); ok {
									results = append(results, text.Text)
								}
							}
						}
					}
					return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}.AssistantMessage(), nil
				}),
			})
			if err := s.session.Prompt(ctx, "again"); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(results, []string{"noop"}) {
				t.Errorf("noop results after the abort = %q", results)
			}
		})
	}
}
