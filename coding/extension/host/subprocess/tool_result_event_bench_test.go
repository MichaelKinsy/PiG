package subprocess

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// BenchmarkNodeToolResultEvent is the tool_result dispatch to a Node extension: its reply carries the edits wrapper, which the host decodes for each handler,
// for scale against BenchmarkNodeToolCallEvent.
func BenchmarkNodeToolResultEvent(b *testing.B) {
	loaded := loadToolResultBenchExtension(b)
	handler := loaded.Handlers["tool_result"][0]
	event := extension.CustomToolResultEvent{
		Type: "tool_result", ToolCallID: "c1", Input: map[string]any{"command": "true"},
		Content:  []any{map[string]any{"type": "text", "text": "ok"}},
		ToolName: "bash",
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := handler(event, b.Context()); err != nil {
			b.Fatal(err)
		}
	}
}

func loadToolResultBenchExtension(b *testing.B) *extension.Extension {
	b.Helper()
	root := b.TempDir()
	source := filepath.Join(root, "tool-result.mjs")
	if err := os.WriteFile(source, []byte(`export default function(pi){let calls=0;pi.on("tool_result",()=>{calls++})}`), 0o600); err != nil {
		b.Fatal(err)
	}
	host := NewHost(root)
	b.Cleanup(func() { host.Shutdown("benchmark done") })
	loaded, err := host.Load(b.Context(), ExtConfig{Name: "tool-result", Source: source, Enabled: true})
	if err != nil {
		b.Fatal(err)
	}
	return loaded
}
