package extensionconformance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// syncLoadoutNodeFactory writes the Node fixture: an extension that does what opts says (see syncLoadoutFactory).
func syncLoadoutNodeFactory(t *testing.T, name string, opts syncLoadoutOptions) subprocess.ExtConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".mjs")
	var code strings.Builder
	code.WriteString("export default function(pi){\nlet seen=" + fmt.Sprintf("%q", syncLoadoutNothingSeen) + ";\n")
	if opts.hook {
		code.WriteString(`pi.registerTool({name:"hooked",label:"hooked",description:"Hooked.",parameters:{type:"object",properties:{}},
prepareLoadout:()=>({descriptions:{hooked:"Hook saw "+seen}}),
execute:async()=>({content:[{type:"text",text:"hooked"}]})});
pi.registerTool({name:"other",label:"other",description:"Other.",parameters:{type:"object",properties:{}},execute:async()=>({content:[{type:"text",text:"other"}]})});
pi.registerTool({name:"extra",label:"extra",description:"Extra.",parameters:{type:"object",properties:{}},execute:async()=>({content:[{type:"text",text:"extra"}]})});
`)
	}
	code.WriteString("pi.on(\"session_start\",async()=>{\n")
	if opts.late {
		code.WriteString(`pi.registerTool({name:"late",label:"late",description:"Late.",parameters:{type:"object",properties:{}},execute:async()=>({content:[{type:"text",text:"late"}]})});
seen="registered";
`)
	}
	if opts.active {
		code.WriteString("pi.setActiveTools([\"hooked\",\"other\"]);\nseen=pi.getActiveTools().join(\"+\");\n")
	}
	code.WriteString("});\n}\n")
	if err := os.WriteFile(path, []byte(code.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return subprocess.ExtConfig{Name: name, Source: path, Enabled: true, Isolation: "shared-ok"}
}

// A tool of a Node extension registers a tool, and Pi's refresh inside registerTool runs the hooks before the call returns (agent-session.ts _refreshToolRegistry -> _applyToolLoadout): the tool sees that its own extension's hook ran, whether or not the model's other tool call of the same batch already waits in the process, and the host reports no failed hook.
func TestNodeHookRunsInsideARegisterToolWhileAnotherCallOfTheBatchWaits(t *testing.T) {
	t.Parallel()
	source := `export default function (pi: any) {
  let runs = 0;
  const schema = { type: "object", properties: {} };
  pi.registerTool({ name: "hooked", label: "H", description: "d", parameters: schema,
    prepareLoadout: () => { runs++; return null; },
    async execute() { return { content: [{ type: "text", text: "ok" }] }; } });
  pi.registerTool({ name: "register_now", label: "R", description: "d", parameters: schema,
    async execute() {
      const before = runs;
      pi.registerTool({ name: "made_late", label: "M", description: "d", parameters: schema, async execute() { return { content: [{ type: "text", text: "ok" }] }; } });
      return { content: [{ type: "text", text: "hook ran inside registerTool: " + (runs > before) }] };
    } });
  pi.registerTool({ name: "noop", label: "N", description: "d", parameters: schema,
    async execute() { return { content: [{ type: "text", text: "noop" }] }; } });
}
`
	for _, batch := range []struct {
		name  string
		calls []string
		want  []string
	}{
		{"alone", []string{"register_now"}, []string{"register_now: hook ran inside registerTool: true"}},
		{"with another call of the batch", []string{"register_now", "noop"}, []string{"register_now: hook ran inside registerTool: true", "noop: noop"}},
	} {
		t.Run(batch.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "extension.ts")
			if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { h.Shutdown("test complete") })
			bridge := subprocess.NewUIBridge(func() {})
			h.SetUIBridge(bridge)
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			loaded, failures := h.LoadAll(ctx, []subprocess.ExtConfig{{Name: "batch", Source: path, Enabled: true, Isolation: "strict"}})
			if len(failures) != 0 || len(loaded) != 1 {
				t.Fatalf("load: %v", failures)
			}
			s := newSyncLoadoutSession(t, ctx, h, bridge, loaded)
			var calls []ai.FauxContentBlock
			for _, name := range batch.calls {
				calls = append(calls, ai.FauxToolCall(name, map[string]any{}, &ai.FauxToolCallOptions{ID: "call-" + name}))
			}
			var results []string
			s.faux.SetResponses([]ai.FauxResponseStep{
				ai.FauxStaticStep(ai.FauxResponse{Content: calls, StopReason: "toolUse"}),
				ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
					for _, message := range request.Messages() {
						if result, ok := message.(ai.ToolResultMessage); ok {
							for _, block := range result.Content {
								if text, ok := block.(ai.TextContent); ok {
									results = append(results, result.ToolName+": "+text.Text)
								}
							}
						}
					}
					return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}.AssistantMessage(), nil
				}),
			})
			if err := s.session.Prompt(ctx, "go"); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(results, batch.want) {
				t.Errorf("tool results = %q, want %q", results, batch.want)
			}
			if reported := s.reported(); len(reported) != 0 {
				t.Errorf("the host reported failures: %q", reported)
			}
		})
	}
}

// nodeSyncLoadoutFixtures are the extensions of #516: the two the reporter gives, byte for byte, the shape of pi-extensible-workflows, and a hook that itself makes a synchronous call while it runs inside the wait (Pi's hook is an ordinary function in the extension's thread, so it may call getActiveTools) (packages/core/src/host.ts: the session_start handler's registerCatalog reads getActiveTools and then registers workflow_catalog, and the `workflow` tool has a prepareLoadout hook that reads the loadout). Each hangs PiG at startup when the extension cannot answer the host's tool_prepare_loadout request while its own synchronous call waits.
var nodeSyncLoadoutFixtures = []struct {
	name, source string
	want         []string
}{
	{"registerTool in session_start", `// repro-loadout-late.ts
export default function (pi: any) {
  pi.registerTool({
    name: "with_loadout", label: "With loadout", description: "has prepareLoadout",
    parameters: { type: "object", properties: {} },
    prepareLoadout: () => null,
    async execute() { return { content: [{ type: "text", text: "ok" }] }; },
  });
  pi.on("session_start", async () => {
    pi.registerTool({
      name: "late_tool", label: "Late", description: "registered during session_start",
      parameters: { type: "object", properties: {} },
      async execute() { return { content: [{ type: "text", text: "ok" }] }; },
    });
  });
}
`, []string{"late_tool: registered during session_start", "with_loadout: has prepareLoadout"}},
	{"setActiveTools then getActiveTools", `// repro-setactive-then-sync.ts
export default function (pi: any) {
  pi.registerTool({ name: "hooked", label: "H", description: "d", parameters: { type: "object", properties: {} },
    prepareLoadout: () => null,
    async execute() { return { content: [{ type: "text", text: "ok" }] }; } });
  pi.on("session_start", async () => {
    pi.setActiveTools(["hooked", "read"]);
    pi.getActiveTools();
  });
}
`, []string{"hooked: d"}},
	{"getActiveTools then registerTool, as pi-extensible-workflows", `export default function (pi: any) {
  pi.registerTool({
    name: "workflow", label: "Workflow", description: "Runs workflows.",
    parameters: { type: "object", properties: {} },
    prepareLoadout(loadout: any) {
      return { descriptions: { workflow: "workflow sees " + loadout.registered.map((tool: any) => tool.name).sort().join("+") } };
    },
    async execute() { return { content: [{ type: "text", text: "ok" }] }; },
  });
  let catalogRegistered = false;
  pi.on("session_start", async () => {
    if (catalogRegistered || !pi.getActiveTools().includes("workflow")) return;
    pi.registerTool({
      name: "workflow_catalog", label: "Workflow Catalog", description: "List reusable workflow functions.",
      parameters: { type: "object", properties: {} },
      async execute() { return { content: [{ type: "text", text: "ok" }] }; },
    });
    catalogRegistered = true;
  });
}
`, []string{"workflow: workflow sees workflow+workflow_catalog", "workflow_catalog: List reusable workflow functions."}},
	{"hook calls getActiveTools inside a registerTool", `export default function (pi: any) {
  pi.registerTool({ name: "hooked", label: "H", description: "d", parameters: { type: "object", properties: {} },
    prepareLoadout: () => ({ descriptions: { hooked: "hook saw active=" + pi.getActiveTools().includes("hooked") } }),
    async execute() { return { content: [{ type: "text", text: "ok" }] }; } });
  pi.on("session_start", async () => {
    pi.registerTool({ name: "late", label: "L", description: "Late.", parameters: { type: "object", properties: {} },
      async execute() { return { content: [{ type: "text", text: "ok" }] }; } });
  });
}
`, []string{"hooked: hook saw active=true", "late: Late."}},
	{"hook calls getActiveTools inside the wait behind setActiveTools", `export default function (pi: any) {
  pi.registerTool({ name: "hooked", label: "H", description: "d", parameters: { type: "object", properties: {} },
    prepareLoadout: () => ({ descriptions: { hooked: "hook saw " + pi.getActiveTools().join("+") } }),
    async execute() { return { content: [{ type: "text", text: "ok" }] }; } });
  pi.on("session_start", async () => {
    pi.setActiveTools(["hooked"]);
    pi.getActiveTools();
  });
}
`, []string{"hooked: hook saw hooked"}},
}

// A Node extension of #516 starts, and its hook runs: in Pi the hook runs inside the registerTool or setActiveTools call (agent-session.ts _applyToolLoadout, from _refreshToolRegistry and setActiveToolsByName), so the session starts and the first request declares what the hooks left.
func TestNodeExtensionsWaitingOnASynchronousCallStartWithTheirPrepareLoadoutHooks(t *testing.T) {
	t.Parallel()
	for _, fixture := range nodeSyncLoadoutFixtures {
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
			loaded, failures := h.LoadAll(ctx, []subprocess.ExtConfig{{Name: "reported", Source: path, Enabled: true, Isolation: "strict"}})
			if len(failures) != 0 || len(loaded) != 1 {
				t.Fatalf("load: %v", failures)
			}
			if declared := syncLoadoutDeclarations(t, ctx, h, bridge, loaded); !slices.Equal(declared, fixture.want) {
				t.Fatalf("declarations = %q, want %q", declared, fixture.want)
			}
		})
	}
}
