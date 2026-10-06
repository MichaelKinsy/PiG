package extensionconformance

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi hands every tool_call handler the one event object. A handler edits event.input in place and the agent loop runs the tool with that object (runner.ts emitToolCall, agent-loop.ts prepareToolCall), so a command rewriter, a guard that normalizes a path or a redactor changes what executes. Every SDK must write its handler's edit back to the Go event's Input, in handler order, whether or not the handler also blocks the call. The in-process Go handler is the reference. The edited values (a rewritten command, a deleted key, an added key, an added nested object) are distinct from the input, so an SDK that never writes its edit back, or that writes back the input it received, cannot match.
func TestToolCallInputMutationAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	newInput := func(command string) map[string]any {
		return map[string]any{"command": command, "drop": "dropped", "timeout": 5.0}
	}
	rewritten := map[string]any{"command": "git status --short", "added": true, "nested": map[string]any{"depth": 2.0}, "timeout": 5.0}
	for _, test := range allHarnessCases() {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			h := test.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			// The model wrote timeout, drop, command. JSON.stringify of Pi's edited object keeps a retained member in its place and appends a new one, so the tool, the next handler and tool_result see the order below, where a sorted map would give added, command, nested, timeout.
			emit := func(tool, command string) (map[string]any, string, *extension.ToolCallEventResult) {
				input := newInput(command)
				wire := json.RawMessage(`{"timeout":5,"drop":"dropped","command":` + mustJSON(t, command) + `}`)
				event := extension.CustomToolCallEvent{
					ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: tool + "-" + command, WireInput: &wire},
					ToolName:          tool,
					Input:             input,
				}
				result, err := h.runner.EmitToolCall(ctx, event)
				if err != nil {
					t.Fatalf("%s tool_call: %v", tool, err)
				}
				after, err := event.InputJSON()
				if err != nil {
					t.Fatalf("%s input: %v", tool, err)
				}
				// An extension process replies with the object it left, and the host stores its bytes in the event so the next handler and the tool see that order. Only a map edit made in the host's own process leaves them stale.
				if strings.HasPrefix(test.name, "subprocess-") && string(wire) != string(after) {
					t.Errorf("%s stored input = %s, want %s", tool, wire, after)
				}
				return input, string(after), result
			}
			const rewrittenWire = `{"timeout":5,"command":"git status --short","added":true,"nested":{"depth":2}}`

			input, wire, result := emit("rewrite_probe", "git status")
			if !reflect.DeepEqual(input, rewritten) {
				t.Errorf("input after the handler rewrote it = %s, want %s", mustJSON(t, input), mustJSON(t, rewritten))
			}
			if wire != rewrittenWire {
				t.Errorf("input members after the handler rewrote it = %s, want %s", wire, rewrittenWire)
			}
			if result != nil && result.Block {
				t.Errorf("a rewriting handler blocked the call: %+v", result)
			}

			input, wire, result = emit("rewrite_probe", "git log")
			if want := newInput("git log"); !reflect.DeepEqual(input, want) {
				t.Errorf("input after a handler that left it alone = %s, want %s", mustJSON(t, input), mustJSON(t, want))
			}
			if want := `{"timeout":5,"drop":"dropped","command":"git log"}`; wire != want {
				t.Errorf("input members after a handler that left it alone = %s, want %s", wire, want)
			}
			if result != nil && result.Block {
				t.Errorf("a handler that left the input blocked the call: %+v", result)
			}

			// Pi runs the tool with the object the event carried when the handlers started (agent-loop.ts prepareToolCall passes the same args to beforeToolCall and execute), so assigning a new object to event.input changes nothing the tool receives. Rust's handler owns the event as a value with no shared object, so assigning to data["input"] is how it edits the input in place, and its fixture has no reassignment probe.
			if test.name != "subprocess-rust" {
				input, wire, result = emit("rewrite_reassign_probe", "git status")
				if want := newInput("git status"); !reflect.DeepEqual(input, want) {
					t.Errorf("input after a handler assigned a new object = %s, want %s", mustJSON(t, input), mustJSON(t, want))
				}
				if want := `{"timeout":5,"drop":"dropped","command":"git status"}`; wire != want {
					t.Errorf("input members after a handler assigned a new object = %s, want %s", wire, want)
				}
				if result != nil && result.Block {
					t.Errorf("a reassigning handler blocked the call: %+v", result)
				}
			}

			// JavaScript keeps an object's insertion order, and so do a Python dict and a serde_json map with preserve_order: moving a member to the end is an edit, and added members follow in the order the handler added them. A Go map has no insertion order, so the Go rows cannot express either edit.
			if !strings.HasSuffix(test.name, "-go") {
				input, wire, _ = emit("rewrite_order_probe", "reorder")
				if want := newInput("reorder"); !reflect.DeepEqual(input, want) {
					t.Errorf("input after a handler moved a member = %s, want %s", mustJSON(t, input), mustJSON(t, want))
				}
				if want := `{"drop":"dropped","command":"reorder","timeout":5}`; wire != want {
					t.Errorf("input members after a handler moved a member = %s, want %s", wire, want)
				}
				_, wire, _ = emit("rewrite_order_probe", "append")
				if want := `{"timeout":5,"drop":"dropped","command":"append","zeta":1,"alpha":2}`; wire != want {
					t.Errorf("input members after a handler added zeta then alpha = %s, want %s", wire, want)
				}
			}

			input, wire, result = emit("rewrite_block_probe", "anything")
			if !reflect.DeepEqual(input, rewritten) {
				t.Errorf("input after a handler that rewrote it and blocked = %s, want %s", mustJSON(t, input), mustJSON(t, rewritten))
			}
			if wire != rewrittenWire {
				t.Errorf("input members after a handler that rewrote it and blocked = %s, want %s", wire, rewrittenWire)
			}
			if result == nil || !result.Block || result.Reason != "blocked after rewrite" {
				t.Errorf("block result = %+v, want blocked after rewrite", result)
			}
		})
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
