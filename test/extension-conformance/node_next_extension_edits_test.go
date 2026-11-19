package extensionconformance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// nextExtensionFirst is the Node extension whose handlers edit the event they are handed in place: a member of the nested data of the tool call's input, a block and the
// details of the tool result, a member of the provider payload, an element pushed to the entries of a turn_end, an element removed from or two elements swapped in the list of
// a context event (chosen by the text of its first message).
const nextExtensionFirst = `export default function (pi) {
  pi.on("tool_call", async (event) => { event.input.nested.value = "EDITED"; });
  pi.on("tool_result", async (event) => { event.content[0].text = "EDITED"; event.details.k = "EDITED"; });
  pi.on("before_provider_request", async (event) => { event.payload.nested.value = "EDITED"; });
  pi.on("turn_end", async (event) => { event.entries.push({ type: "custom", customType: "from-first" }); });
  pi.on("context", async (event) => {
    const first = event.messages[0].content;
    if (first === "remove") event.messages.splice(0, 1);
    if (first === "swap") event.messages.reverse();
  });
}
`

// nextExtensionSecond is the extension loaded after it: its handlers report what they read of the same data.
const nextExtensionSecond = `export default function (pi) {
  const report = (ctx, name, value) => ctx.ui.notify("edit_next:" + name + ":" + JSON.stringify(value) + ":end", "info");
  pi.on("tool_call", async (event, ctx) => report(ctx, "tool_call", event.input.nested.value));
  pi.on("tool_result", async (event, ctx) => report(ctx, "tool_result", [event.content[0].text, event.details.k]));
  pi.on("before_provider_request", async (event, ctx) => report(ctx, "before_provider_request", event.payload.nested.value));
  pi.on("turn_end", async (event, ctx) => report(ctx, "turn_end", event.entries.map((e) => e.customType)));
  pi.on("context", async (event, ctx) => report(ctx, "context", event.messages.map((m) => m.content)));
}
`

// makeTwoNodeExtensionHarness loads two Node extensions of one packed cell, first before second.
func makeTwoNodeExtensionHarness(t *testing.T, first, second string) *harness {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for SDK conformance: %v", err)
	}
	dir := t.TempDir()
	paths := make([]string, 2)
	for i, source := range []string{first, second} {
		paths[i] = filepath.Join(dir, []string{"edit-first.mjs", "edit-next.mjs"}[i])
		if err := os.WriteFile(paths[i], []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	notify, status, actions := &[]string{}, &[]string{}, &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(hostEffects.notify)
	bridge.SetUIContext(ui)
	bridge.SetNotifyFunc(ui.RecordNotify)
	bridge.SetActions(conformanceActions(actions))
	h := subprocess.NewHost(harnessHostCwd(t))
	h.SetMode(harnessHostMode())
	h.SetHostInfo(conformanceHostInfo)
	h.ShareCells(conformanceCellRegistry)
	h.SetUIBridge(bridge)
	wire := observeWire(t, h)
	exts, errs := h.LoadAll(context.Background(), []subprocess.ExtConfig{
		{Name: "node-edit-first", Source: paths[0], Enabled: true},
		{Name: "node-edit-next", Source: paths[1], Enabled: true},
	})
	if len(errs) != 0 || len(exts) != 2 {
		t.Fatalf("two Node extensions: %d loaded, %v", len(exts), errs)
	}
	runner := inproc.NewRunner(exts, t.TempDir())
	bridge.SetUIPromptScope(runner)
	t.Cleanup(func() { h.Shutdown("test done") })
	return &harness{runner: runner, host: h, notify: notify, status: status, actions: actions, ui: ui, bridge: bridge, wire: wire}
}

// TestNodeInPlaceEditsReachTheNextExtension pins, for the Node runtime, what the handler of the SECOND extension of a cell reads after the first extension's handler edited the
// event in place, through the Runner calls the Session makes. In Pi every handler of a dispatch holds the same objects, whatever extension it belongs to (runner.ts emitToolCall
// 1256, emitToolResult 1197, emitBeforeProviderRequest 1375, emitBoundary 1043, emitContext 1312), so the second extension reads each edit.
func TestNodeInPlaceEditsReachTheNextExtension(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	user := func(text string) extension.AgentMessage {
		return wireAgentMessage(map[string]any{"role": "user", "content": text, "timestamp": float64(1)})
	}
	for _, tc := range []struct {
		name, event, want string
		emit              func(t *testing.T, h *harness) error
	}{
		{"tool_call input", "tool_call", `"EDITED"`, func(t *testing.T, h *harness) error {
			_, err := h.runner.EmitToolCall(t.Context(), extension.CustomToolCallEvent{Type: "tool_call", ToolCallID: "c1", ToolName: "probe", Input: map[string]any{"nested": map[string]any{"value": "orig"}}})
			return err
		}},
		{"tool_result content and details", "tool_result", `["EDITED","EDITED"]`, func(t *testing.T, h *harness) error {
			_, err := h.runner.EmitToolResult(t.Context(), extension.CustomToolResultEvent{
				Type: "tool_result", ToolCallID: "c1", ToolName: "probe", Input: map[string]any{},
				Content: []any{map[string]any{"type": "text", "text": "orig"}}, Details: json.RawMessage(`{"k":"orig"}`),
			})
			return err
		}},
		{"before_provider_request payload", "before_provider_request", `"EDITED"`, func(t *testing.T, h *harness) error {
			_, err := h.runner.EmitBeforeProviderRequest(t.Context(), map[string]any{"nested": map[string]any{"value": "orig"}})
			return err
		}},
		{"turn_end entries", "turn_end", `["from-first"]`, func(t *testing.T, h *harness) error {
			base := extension.TurnEndEvent{Type: "turn_end", TurnIndex: 1, MessageEntryID: "m", Message: wireAgentMessage(map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "x"}}, "stopReason": "stop"}), BoundaryState: &extension.BoundaryState{Outcome: extension.AgentActivityError}}
			_, err := h.runner.EmitBoundary(t.Context(), base, func([]extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
				return extension.BoundaryContextPreview{ContextEntries: []extension.ProjectedSessionEntry{}, ContextMessages: []extension.AgentMessage{}}, nil
			})
			return err
		}},
		{"context element removed", "context", `["b"]`, func(t *testing.T, h *harness) error {
			_, err := h.runner.EmitContext(t.Context(), []extension.AgentMessage{user("remove"), user("b")})
			return err
		}},
		{"context elements swapped", "context", `["b","swap"]`, func(t *testing.T, h *harness) error {
			_, err := h.runner.EmitContext(t.Context(), []extension.AgentMessage{user("swap"), user("b")})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := makeTwoNodeExtensionHarness(t, nextExtensionFirst, nextExtensionSecond)
			if err := tc.emit(t, h); err != nil {
				t.Fatal(err)
			}
			prefix := "edit_next:" + tc.event + ":"
			var got string
			pollUntilConformance(t, "the second extension reports", func() bool {
				for _, n := range h.ui.Recorded() {
					if rest, ok := strings.CutPrefix(n, prefix); ok {
						got = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
						return true
					}
				}
				return false
			})
			if got != tc.want {
				t.Errorf("the second extension's handler read %s, want %s (the first extension's in-place edit)", got, tc.want)
			}
		})
	}
}
