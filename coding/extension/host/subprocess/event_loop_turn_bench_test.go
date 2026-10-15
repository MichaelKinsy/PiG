package subprocess

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// BenchmarkNodeEventLoopTurn is the cost a tool call pays for each Node extension with a tool_call handler: one event-loop turn of
// its process (RequestEventLoopTurn, then the host applying the calls sent before NotifyEventLoopTurned). BenchmarkNodeToolCallEvent
// is the tool_call dispatch the same call already pays, for scale.
func BenchmarkNodeEventLoopTurn(b *testing.B) {
	loaded := loadToolCallBenchExtension(b)
	if loaded.EventLoopTurn == nil {
		b.Fatal("a Node extension has no EventLoopTurn")
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := loaded.EventLoopTurn(b.Context()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNodeToolCallEvent(b *testing.B) {
	loaded := loadToolCallBenchExtension(b)
	handler := loaded.Handlers["tool_call"][0]
	event := map[string]any{"type": "tool_call", "toolCallId": "c1", "toolName": "bash", "input": map[string]any{"command": "true"}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := handler(event, b.Context()); err != nil {
			b.Fatal(err)
		}
	}
}

func loadToolCallBenchExtension(b *testing.B) *extension.Extension {
	b.Helper()
	root := b.TempDir()
	source := filepath.Join(root, "tool-call.mjs")
	if err := os.WriteFile(source, []byte(`export default function(pi){let calls=0;pi.on("tool_call",()=>{calls++})}`), 0o600); err != nil {
		b.Fatal(err)
	}
	host := NewHost(root)
	b.Cleanup(func() { host.Shutdown("benchmark done") })
	loaded, err := host.Load(b.Context(), ExtConfig{Name: "tool-call", Source: source, Enabled: true})
	if err != nil {
		b.Fatal(err)
	}
	return loaded
}
