package subprocess

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const nodeContextIdentityExtension = `export default function (pi) {
  pi.on("context", (event) => {
    if (event.messages[0].content === "replace") return { messages: [{ ...event.messages[0], content: "edited" }, event.messages[1]] };
    event.messages[0].content = "edited";
    if (event.messages[0].timestamp === 1) return { messages: event.messages };
  });
}
`

// Source: runner.ts emitContext/sameMessages. An SDK handler's in-place edit
// (returning nothing or the same list) crosses the wire as an unchanged
// conversation, so the Session keeps every system message in place; a new
// list is a replacement. REFNL-003.
func TestContextIdentityCrossesSDKTransports(t *testing.T) {
	loaders := map[string]func(t *testing.T) extension.Extension{
		"fused-go": func(t *testing.T) extension.Extension {
			ext := sdk.New("context-identity")
			ext.OnEvent("context", func(_ sdk.Context, data map[string]any) (any, error) {
				messages := data["messages"].([]any)
				first := messages[0].(map[string]any)
				if first["content"] == "replace" {
					copied := map[string]any{"role": first["role"], "content": "edited", "timestamp": first["timestamp"]}
					return map[string]any{"messages": []any{copied, messages[1]}}, nil
				}
				first["content"] = "edited"
				if first["timestamp"] == float64(1) {
					return map[string]any{"messages": messages}, nil
				}
				return nil, nil
			})
			host := NewHost(t.TempDir())
			t.Cleanup(func() { host.Shutdown("test complete") })
			loaded, err := host.LoadInProcess(t.Context(), ExtConfig{Name: "context-identity", Enabled: true}, ext.RunWithConn)
			if err != nil {
				t.Fatal(err)
			}
			return *loaded
		},
		"node": func(t *testing.T) extension.Extension {
			if _, err := exec.LookPath("node"); err != nil {
				t.Fatalf("node is required: %v", err)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "index.mjs"), []byte(nodeContextIdentityExtension), 0o644); err != nil {
				t.Fatal(err)
			}
			host := NewHost(t.TempDir())
			t.Cleanup(func() { host.Shutdown("test complete") })
			loaded, err := host.Load(testbudget.Context(t), ExtConfig{Name: "context-identity", Source: filepath.Join(dir, "index.mjs"), Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			return *loaded
		},
	}
	for name, load := range loaders {
		t.Run(name, func(t *testing.T) {
			runner := inproc.NewRunner([]extension.Extension{load(t)}, t.TempDir())
			defer runner.Invalidate("test complete")
			for _, tc := range []struct {
				name         string
				content      string
				timestamp    int
				wantReplaced bool
			}{
				{name: "in place", content: "original", timestamp: 0},
				{name: "same list", content: "original", timestamp: 1},
				{name: "replacement", content: "replace", timestamp: 0, wantReplaced: true},
			} {
				messages := []extension.AgentMessage{
					map[string]any{"role": "user", "content": tc.content, "timestamp": tc.timestamp},
					map[string]any{"role": "user", "content": "two", "timestamp": 0},
				}
				got, replaced, err := runner.EmitContextTracked(t.Context(), messages)
				if err != nil {
					t.Fatalf("%s: %v", tc.name, err)
				}
				if replaced != tc.wantReplaced {
					t.Fatalf("%s: replaced = %t, want %t", tc.name, replaced, tc.wantReplaced)
				}
				if len(got) != 2 || got[0].(map[string]any)["content"] != "edited" || got[1].(map[string]any)["content"] != "two" {
					t.Fatalf("%s: context = %#v", tc.name, got)
				}
				if messages[0].(map[string]any)["content"] != tc.content {
					t.Fatalf("%s: context transform mutated its input", tc.name)
				}
			}
		})
	}
}
