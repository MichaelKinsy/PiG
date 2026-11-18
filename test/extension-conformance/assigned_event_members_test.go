package extensionconformance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// assignedEventCase is one event whose handler ASSIGNS a new value to a member of the event (the event_assign_probe handlers) instead of editing the old value in
// place, and what Pi does with it: seen is what the next handler of the chain reads, caller what the caller of the emit is left with. carried is what both are
// when the SDK carries the assigned member, as the native SDKs do for a context list and the entries of turn_end; nil for tool_call.
type assignedEventCase struct {
	event, field          string
	seen, caller, carried map[string]any
}

func assignedEventCases() []assignedEventCase {
	user := func(text string) map[string]any {
		return map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}, "timestamp": float64(1)}
	}
	return []assignedEventCase{
		// Pi hands every tool_call handler the one event, so the next handler reads the input the first assigned; the agent loop runs the tool with the args object it
		// handed the event (agent-loop.ts prepareToolCall passes the same args to beforeToolCall and execute), which the assignment does not touch.
		{event: "tool_call", field: "input", seen: map[string]any{"input": map[string]any{"command": inPlaceEdited}}, caller: map[string]any{"input": map[string]any{"command": "orig"}}},
		// emitContext makes a fresh event for each handler from its own list and takes a list only from the handler's result or an in-place edit of the list it handed.
		{event: "context", field: "messages", seen: map[string]any{"messages": []any{user("orig")}}, caller: map[string]any{"messages": []any{user("orig")}},
			carried: map[string]any{"messages": []any{user(inPlaceEdited)}}},
		// emitBoundary makes a fresh event for each handler and takes entries only from the handler's result.
		{event: "turn_end", field: "entries", seen: map[string]any{"entries": []any{}}, caller: map[string]any{"entries": []any{}},
			carried: map[string]any{"entries": []any{map[string]any{"type": "custom", "customType": inPlaceEdited}}}},
	}
}

// TestAssignedEventMembersFollowPi requires every SDK to leave what Pi leaves when a handler assigns a new value to event.input of tool_call, event.messages of context
// or event.entries of turn_end (TestAssignedEventMembersMatchPi runs Pi's runner over the same assignments).
func TestAssignedEventMembersFollowPi(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			for _, c := range assignedEventCases() {
				t.Run(c.event, func(t *testing.T) {
					h := tc.make(t)
					t.Cleanup(func() {
						if h.cleanup != nil {
							h.cleanup()
						}
						if h.host != nil {
							h.host.Shutdown("test done")
						}
					})
					runConformanceCommandArgs(t, h, "event_assign_probe", c.event)
					runConformanceCommandArgs(t, h, "event_field_probe", c.event+" "+c.field)
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					var got map[string]any
					switch c.event {
					case "tool_call":
						wire := json.RawMessage(`{"command":"orig"}`)
						event := extension.CustomToolCallEvent{Type: "tool_call", ToolCallID: "c1", ToolName: "probe", WireInput: &wire, Input: map[string]any{"command": "orig"}}
						if _, err := h.runner.EmitToolCall(ctx, event); err != nil {
							t.Fatal(err)
						}
						after, err := event.InputJSON()
						if err != nil {
							t.Fatal(err)
						}
						got = map[string]any{"input": decodeAny(t, json.RawMessage(after))}
					case "context":
						returned, err := h.runner.EmitContext(ctx, []extension.AgentMessage{agentMessageFromJSON(t, `{"role":"user","content":[{"type":"text","text":"orig"}],"timestamp":1}`)})
						if err != nil {
							t.Fatal(err)
						}
						got = map[string]any{"messages": decodeAny(t, returned)}
					case "turn_end":
						base := extension.TurnEndEvent{Type: "turn_end", TurnIndex: 1, MessageEntryID: "m", ToolResultEntryIds: []string{},
							Message:     wireAgentMessage(map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "done"}}, "stopReason": "stop"}),
							ToolResults: []extension.ToolResultMessage{}, BoundaryState: &extension.BoundaryState{Outcome: extension.AgentActivityCompleted}}
						build := func([]extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
							return extension.BoundaryContextPreview{ContextEntries: []extension.ProjectedSessionEntry{}, ContextMessages: []extension.AgentMessage{}, LLMMessages: []ai.Message{},
								PendingMessages: []extension.AgentMessage{}, CanContinue: true}, nil
						}
						result, err := h.runner.EmitBoundary(ctx, base, build)
						if err != nil {
							t.Fatal(err)
						}
						entries := result.Entries
						if entries == nil {
							entries = []extension.SessionBoundaryDraft{}
						}
						got = map[string]any{"entries": decodeAny(t, entries)}
					}
					wantCaller, wantSeen := c.caller, c.seen
					switch {
					case c.event == "tool_call" && strings.Contains(tc.name, "rust"):
						// A Rust handler holds the event as one Value, so assigning data["input"] and editing it are the same edit, and the call's input is what the handler left.
						wantCaller = map[string]any{"input": map[string]any{"command": inPlaceEdited}}
					case c.carried != nil && !strings.HasPrefix(tc.name, "subprocess-node"):
						// The native SDKs carry the assigned list (the Go and Rust SDKs since v0.2.0 / v0.3.0, Lisp's turn_end since v0.7.0), the next handler and the request or session hold it; the Node runtime follows Pi.
						wantCaller, wantSeen = c.carried, c.carried
					}
					if !reflect.DeepEqual(got, wantCaller) {
						t.Errorf("%s %s: what the caller is left with\n got %v\nwant %v", tc.name, c.event, got, wantCaller)
					}
					seen := map[string]any{c.field: reportedField(t, h, c.event, c.field)}
					if !reflect.DeepEqual(seen, wantSeen) {
						t.Errorf("%s %s: what the next handler of the chain sees\n got %v\nwant %v", tc.name, c.event, seen, wantSeen)
					}
				})
			}
		})
	}
}

// TestAssignedContextListIsValidatedLikeAReturnedOne requires a native SDK that carries an assigned event.messages to treat a bad one (a string, a list holding a
// number) as it treats the same value returned from the handler: the host answers both alike, whether it refuses them or not. The Node runtime does not carry an assignment.
func TestAssignedContextListIsValidatedLikeAReturnedOne(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		if strings.HasPrefix(tc.name, "subprocess-node") {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			for _, bad := range []struct{ variant, returned string }{{"bad", `{"messages":"oops"}`}, {"badmsg", `{"messages":[42]}`}} {
				t.Run(bad.variant, func(t *testing.T) {
					outcome := func(setup func(h *harness)) string {
						h := tc.make(t)
						t.Cleanup(func() {
							if h.cleanup != nil {
								h.cleanup()
							}
							if h.host != nil {
								h.host.Shutdown("test done")
							}
						})
						setup(h)
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						returned, err := h.runner.EmitContext(ctx, []extension.AgentMessage{agentMessageFromJSON(t, `{"role":"user","content":[{"type":"text","text":"orig"}],"timestamp":1}`)})
						if err != nil {
							return "error: " + err.Error()
						}
						return "messages: " + string(mustMarshal(t, returned))
					}
					assigned := outcome(func(h *harness) { runConformanceCommandArgs(t, h, "event_assign_probe", "context "+bad.variant) })
					returned := outcome(func(h *harness) {
						runConformanceCommandArgs(t, h, "event_probe_result", "context "+bad.returned)
						runConformanceCommandArgs(t, h, "event_probe_on", "context")
					})
					if assigned != returned {
						t.Errorf("%s: a handler that assigned the bad list left\n %s\nwhere the same value returned left\n %s", tc.name, assigned, returned)
					}
				})
			}
		})
	}
}

// TestAssignedEventMembersMatchPi runs Pi's own ExtensionRunner over handlers that assign event.input, event.messages and event.entries, and requires what it leaves
// the caller and the next handler to be what the rows of TestAssignedEventMembersFollowPi require of every SDK.
func TestAssignedEventMembersMatchPi(t *testing.T) {
	t.Parallel()
	runner, err := filepath.Abs("../../coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-coding-agent/core/extensions/runner.js")
	if err != nil {
		t.Fatal(err)
	}
	const body = `
const { ExtensionRunner } = await import(input.runner);
const edited = "other-event-edit";
const assign = {
	tool_call: (e) => { e.input = { command: edited }; },
	context: (e) => { e.messages = [{ role: "user", content: [{ type: "text", text: edited }], timestamp: 1 }]; },
	turn_end: (e) => { e.entries = [{ type: "custom", customType: edited }]; },
};
const out = {};
for (const c of input.cases) {
	const seen = {};
	const observe = async (e) => { seen[c.field] = e[c.field]; };
	const handlers = new Map([[c.event, [async (e) => assign[c.event](e), observe]]]);
	const pi = new ExtensionRunner([{ path: "/probe.mjs", handlers }], {}, "/", undefined, undefined);
	pi.onError?.(() => {});
	let caller;
	if (c.event === "tool_call") {
		const args = { command: "orig" };
		await pi.emitToolCall({ type: "tool_call", toolName: "probe", toolCallId: "c1", input: args });
		caller = { input: args };
	} else if (c.event === "context") {
		caller = { messages: await pi.emitContext([{ role: "user", content: [{ type: "text", text: "orig" }], timestamp: 1 }]) };
	} else {
		const base = { type: "turn_end", turnIndex: 1, message: { role: "assistant", content: [{ type: "text", text: "done" }], stopReason: "stop" }, toolResults: [] };
		const result = await pi.emitBoundary(base, async () => ({ contextEntries: [], contextMessages: [], llmMessages: [], pendingMessages: [], canContinue: true }));
		caller = { entries: result.entries };
	}
	out[c.event] = { seen: JSON.parse(JSON.stringify(seen)), caller: JSON.parse(JSON.stringify(caller)) };
}
return out;
`
	cases := assignedEventCases()
	var wire []map[string]any
	for _, c := range cases {
		wire = append(wire, map[string]any{"event": c.event, "field": c.field})
	}
	var got map[string]struct {
		Seen   map[string]any
		Caller map[string]any
	}
	out := testenv.PiEvalJSON(t, body, map[string]any{"runner": "file://" + runner, "cases": wire})
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if !reflect.DeepEqual(got[c.event].Seen, c.seen) {
			t.Errorf("%s: what Pi's next handler sees\n got %v\nwant %v", c.event, got[c.event].Seen, c.seen)
		}
		if !reflect.DeepEqual(got[c.event].Caller, c.caller) {
			t.Errorf("%s: what Pi leaves its caller\n got %v\nwant %v", c.event, got[c.event].Caller, c.caller)
		}
	}
}
