package subprocess

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// BenchmarkNodeToolResultEvent is the tool_result dispatch to a Node extension whose handler edits nothing: every handler's reply carries the edits wrapper, which the host
// unwraps for each handler, for scale against BenchmarkNodeToolCallEvent.
func BenchmarkNodeToolResultEvent(b *testing.B) {
	benchToolResult(b, `export default function(pi){let calls=0;pi.on("tool_result",()=>{calls++})}`)
}

// BenchmarkNodeToolResultEventEdit is the same dispatch to a handler that edits one member of the event (isError), so its reply carries the edits.
func BenchmarkNodeToolResultEventEdit(b *testing.B) {
	benchToolResult(b, `export default function(pi){pi.on("tool_result",(event)=>{event.isError=true})}`)
}

// BenchmarkNodeMessageEndEvent is the message_end dispatch to a Node extension whose handler edits nothing; its reply carries the message wrapper.
func BenchmarkNodeMessageEndEvent(b *testing.B) {
	benchMessageEnd(b, `export default function(pi){let calls=0;pi.on("message_end",()=>{calls++})}`)
}

// BenchmarkNodeMessageEndEventEdit is the same dispatch to a handler that edits the message in place.
func BenchmarkNodeMessageEndEventEdit(b *testing.B) {
	benchMessageEnd(b, `export default function(pi){let n=2;pi.on("message_end",(event)=>{event.message.timestamp=n++})}`)
}

func benchToolResult(b *testing.B, source string) {
	b.Helper()
	handler := loadNodeBenchExtension(b, "tool_result", source)
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

func benchMessageEnd(b *testing.B, source string) {
	b.Helper()
	handler := loadNodeBenchExtension(b, "message_end", source)
	message := agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("hello"), Timestamp: 1}}
	ctx := extension.WithMessageSequences(context.Background(), func() []uint64 { return []uint64{9001} })
	b.ReportAllocs()
	for b.Loop() {
		if _, err := handler(extension.MessageEndEvent{Type: "message_end", Message: message}, ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func loadNodeBenchExtension(b *testing.B, event, source string) extension.HandlerFn {
	b.Helper()
	root := b.TempDir()
	path := filepath.Join(root, event+".mjs")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		b.Fatal(err)
	}
	host := NewHost(root)
	b.Cleanup(func() { host.Shutdown("benchmark done") })
	loaded, err := host.Load(b.Context(), ExtConfig{Name: event, Source: path, Enabled: true})
	if err != nil {
		b.Fatal(err)
	}
	return loaded.Handlers[event][0]
}
