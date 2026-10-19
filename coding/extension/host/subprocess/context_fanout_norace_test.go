//go:build !race && !pig_strip_node_extensions

package subprocess

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// contextHistoryMessages is exchanges of a user line, an assistant answer with a tool call, and the tool's result.
func contextHistoryMessages(exchanges int) []extension.AgentMessage {
	messages := make([]extension.AgentMessage, 0, 3*exchanges)
	for i := range exchanges {
		messages = append(messages,
			extension.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText(fmt.Sprintf("question %d about the repository layout", i))}},
			extension.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "I will read the file."}, ai.ToolCall{ID: fmt.Sprintf("call_%d", i), Name: "read", Arguments: ai.JsonObject{"path": "f.go"}}}, StopReason: ai.StopReasonToolUse}},
			extension.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: fmt.Sprintf("call_%d", i), ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "package main"}}}},
		)
	}
	return messages
}

// contextFanoutAllocs is the allocations one `context` dispatch of messages makes in this process (the least of a few runs) with handlers no-op Node extensions loaded.
func contextFanoutAllocs(t *testing.T, handlers int, messages []extension.AgentMessage) float64 {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required: %v", err)
	}
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test complete") })
	source := filepath.Join(t.TempDir(), "index.mjs")
	if err := os.WriteFile(source, []byte(`export default function (pi) { pi.on("context", () => {}); }`), 0o644); err != nil {
		t.Fatal(err)
	}
	var loaded []extension.Extension
	for i := range handlers {
		name := fmt.Sprintf("fanout-%d", i)
		ext, err := host.Load(testbudget.Context(t), ExtConfig{Name: name, Source: source, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		loaded = append(loaded, *ext)
	}
	runner := inproc.NewRunner(loaded, t.TempDir())
	defer runner.Invalidate("test complete")
	emit := func() {
		if _, replaced, err := runner.EmitContextTracked(t.Context(), messages); err != nil || replaced {
			t.Fatalf("replaced = %t, err = %v", replaced, err)
		}
	}
	emit()
	best := 1e18
	for range 3 {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		emit()
		runtime.ReadMemStats(&after)
		best = min(best, float64(after.Mallocs-before.Mallocs))
	}
	return best
}

// A `context` dispatch encodes the conversation once for every handler of the dispatch, whatever extension process they run in: a handler that changed nothing leaves the event as the next handler receives it (eventwire), and its reply carries no list to decode. Four handlers cost the dispatch what one does, plus a frame each; before, each handler's event was encoded again and its echoed list decoded again (2.7x the allocations of one).
func TestContextDispatchEncodesOncePerEmitForEveryHandler(t *testing.T) {
	messages := contextHistoryMessages(100)
	one := contextFanoutAllocs(t, 1, messages)
	four := contextFanoutAllocs(t, 4, messages)
	t.Logf("allocations of one dispatch over %d messages: 1 handler %.0f, 4 handlers %.0f", len(messages), one, four)
	if four > 1.25*one {
		t.Fatalf("four handlers allocate %.0f against %.0f for one (%.2fx): the dispatch encodes or decodes per handler", four, one, four/one)
	}
}
