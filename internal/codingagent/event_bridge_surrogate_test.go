package codingagent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Pi hands an extension the parsed arguments the model wrote and the tool's own result object, so a lone UTF-16 unit in either (JSON.parse reads
// `\ud800` as that unit) reaches an in-process handler as the unit, not as U+FFFD.
func TestToolExecutionEventsKeepALoneSurrogateForInProcessHandlers(t *testing.T) {
	lone := jsstring.FromUTF16([]uint16{0xd800})
	const args = `{"text":"a\ud800<&>","nested":{"k":["\ud800"]}}`
	events := make(chan any, 4)
	handler := func(args ...any) (any, error) {
		events <- args[0]
		return nil, nil
	}
	runner := inproc.NewRunner([]extension.Extension{{
		Path: "/fixture/surrogate", ResolvedPath: "/fixture/surrogate",
		Handlers: map[string][]extension.HandlerFn{
			EventToolExecutionStart:  {handler},
			EventToolExecutionUpdate: {handler},
			EventToolExecutionEnd:    {handler},
		},
	}}, ".")
	checkArgs := func(t *testing.T, got any) {
		t.Helper()
		members, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("args = %#v, want the decoded object", got)
		}
		if text, _ := members["text"].(string); text != "a"+lone+"<&>" {
			t.Errorf("args.text units = %x, want %x", jsstring.ToUTF16(text), jsstring.ToUTF16("a"+lone+"<&>"))
		}
		nested, _ := members["nested"].(map[string]any)
		list, _ := nested["k"].([]any)
		if len(list) != 1 || list[0] != lone {
			t.Errorf("args.nested.k = %#v, want [%x]", list, jsstring.ToUTF16(lone))
		}
	}
	structured := json.RawMessage(`{"s":"x\udc00<"}`)
	checkStructured := func(t *testing.T, result any) {
		t.Helper()
		members, _ := result.(map[string]any)
		inner, _ := members["structuredContent"].(map[string]any)
		if text, _ := inner["s"].(string); text != "x"+jsstring.FromUTF16([]uint16{0xdc00})+"<" {
			t.Errorf("structuredContent.s units = %x, want x, dc00, <", jsstring.ToUTF16(text))
		}
	}

	emitToolExecutionStart(runner, "call", "bash", json.RawMessage(args), "")
	start, ok := (<-events).(extension.ToolExecutionStartEvent)
	if !ok {
		t.Fatal("no tool_execution_start event")
	}
	t.Run("start args", func(t *testing.T) { checkArgs(t, start.Args) })

	emitToolExecutionUpdate(runner, "call", "bash", agent.AgentToolResult{StructuredContent: structured}, json.RawMessage(args), "")
	update, ok := (<-events).(extension.ToolExecutionUpdateEvent)
	if !ok {
		t.Fatal("no tool_execution_update event")
	}
	t.Run("update args", func(t *testing.T) { checkArgs(t, update.Args) })
	t.Run("update partial result", func(t *testing.T) { checkStructured(t, update.PartialResult) })

	emitToolExecutionEnd(runner, agent.ToolExecutionEndEvent{ToolCallID: "call", ToolName: "bash", Result: agent.AgentToolResult{StructuredContent: structured}})
	end, ok := (<-events).(extension.ToolExecutionEndEvent)
	if !ok {
		t.Fatal("no tool_execution_end event")
	}
	t.Run("end result", func(t *testing.T) { checkStructured(t, end.Result) })
}
