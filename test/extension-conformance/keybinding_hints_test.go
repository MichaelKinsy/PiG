package extensionconformance

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

var keyHintANSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// pig divergence (D217): Pi 9376c8d1 (#4748, packages/coding-agent/test/extension-keybinding-hints.test.ts) hands renderers the host's keyText and
// keyHint on the tool render context and the message and entry render options, so an extension formats the user's keybindings without importing a
// copy of the package that does not see them. Each renderer prints `keyText | keyHint` (ANSI stripped) for the keybinding id it is given, and
// every expectation differs from what an SDK that returns "" or reads the defaults prints:
//   - app.tools.expand is overridden to ctrl+e (Pi's test); the default is ctrl+o.
//   - app.session.rename is unbound by the user, so keyText is "" and the hint is the description alone.
//   - tui.select.cancel keeps its two default keys, "escape/ctrl+c".
//
// Every SDK, isolated and packed, and the in-process reference must print the same strings on every render surface.
func TestKeybindingHintsAcrossSDKs(t *testing.T) {
	rows := []struct{ id, want string }{
		{"app.tools.expand", "ctrl+e | ctrl+e to expand"},
		{"app.session.rename", " |  to expand"},
		{"tui.select.cancel", "escape/ctrl+c | escape/ctrl+c to expand"},
	}
	cases := append(allHarnessCases(), packedSDKHarnessCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			// The host's keybindings: the global registry the in-process reference reads and the table every state snapshot carries to a subprocess.
			manager := codingagent.NewKeybindingsManagerFromBindings(map[string][]string{"app.tools.expand": {"ctrl+e"}, "app.session.rename": {}}, "")
			t.Cleanup(func() { codingagent.DefaultKeybindingsManager() })
			if h.bridge != nil {
				table := manager.ExtensionKeybindingTable()
				h.bridge.SetKeybindingsFunc(func() any { return table })
				t.Cleanup(func() { h.bridge.SetKeybindingsFunc(nil) })
				h.host.BroadcastStateUpdate()
			}

			expect := func(t *testing.T, surface, id, want string, draw func() []string) {
				t.Helper()
				var got []string
				ok := awaitCondition(testbudget.Context(t), func() bool {
					lines := draw()
					got = nil
					for _, line := range lines {
						got = append(got, keyHintANSI.ReplaceAllString(line, ""))
					}
					return len(got) == 1 && got[0] == want
				})
				if !ok {
					t.Errorf("%s %s: lines = %q, want [%q]", surface, id, got, want)
				}
			}
			lines := func(component any) func() []string {
				return func() []string {
					drawn, ok := component.(interface{ Render(int) []string })
					if !ok {
						t.Fatalf("renderer returned %T, want a component", component)
					}
					return drawn.Render(72)
				}
			}

			messageRenderer := h.runner.MessageRenderer("keyhints-message")
			entryRenderer := h.runner.EntryRenderer("keyhints-entry")
			if messageRenderer == nil || entryRenderer == nil {
				t.Fatalf("keyhints renderers missing: message=%v entry=%v", messageRenderer != nil, entryRenderer != nil)
			}
			base := &extension.ToolRenderers{RenderShell: extension.ToolRenderShellDefault}
			var tool *extension.ToolRenderers
			waitFor(t, func() bool {
				tool = h.runner.ResolveToolRenderers("conformance_keyhints", func() *extension.ToolRenderers { return base })
				return tool != base
			})
			if tool == nil || tool.RenderCall == nil {
				t.Fatalf("resolved renderers = %+v, want the keyhints call renderer", tool)
			}

			for _, row := range rows {
				expect(t, "message options", row.id, row.want, lines(messageRenderer(
					extension.CustomMessage{CustomType: "keyhints-message", Content: row.id, Display: true}, extension.MessageRenderOptions{Expanded: true, OutputPad: 1}, nil)))
				expect(t, "entry options", row.id, row.want, lines(entryRenderer(
					extension.CustomEntry{CustomType: "keyhints-entry", Data: row.id}, extension.EntryRenderOptions{Expanded: true}, nil)))
				args, err := json.Marshal(map[string]any{"q": row.id})
				if err != nil {
					t.Fatal(err)
				}
				renderContext := extension.ToolRenderContext{
					Args: json.RawMessage(args), ToolCallID: "keyhints-" + row.id, Card: "keyhints-card-" + row.id,
					State: map[string]any{}, Invalidate: func() {}, ExecutionStarted: true, ArgsComplete: true,
				}
				expect(t, "tool render context", row.id, row.want, lines(tool.RenderCall(json.RawMessage(args), nil, renderContext)))
			}
		})
	}
}
