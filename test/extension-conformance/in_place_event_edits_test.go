package extensionconformance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// inPlaceEdited is the value the event_edit_probe handlers write; no other test uses it.
const inPlaceEdited = "other-event-edit"

// eventEditCase is one handler that edits an event it is handed and returns nothing, and what Pi does with the edit. The edits are those of Pi's own runner
// (TestInPlaceEventEditsMatchPi runs them): a later handler of the chain sees every edit, and the caller keeps an edit made to the object the caller handed
// over (a block of the content, a member of the details, the message) but not a member the handler assigned another value (runner.ts emitToolResult returns
// currentEvent's members only when a handler returned a result, and gives each message_end handler a fresh event).
type eventEditCase struct {
	event, edit string
	fail        bool
	// seen is what the next handler of the chain sees; caller is what the caller of the emit is left with.
	seen, caller map[string]any
	// assigns is set for an edit that assigns a member the handler's data holds (Rust's `data["content"] = ...` is also how it edits the content in place).
	assigns bool
}

// inPlaceToolResult is the tool result every tool_result row hands the handlers.
func inPlaceToolResult(content string, details string, isError bool) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": content}},
		"details": map[string]any{"k": details},
		"isError": isError,
	}
}

func eventEditCases() []eventEditCase {
	orig := inPlaceToolResult("orig", "orig", false)
	edited := inPlaceToolResult
	message := func(text string) map[string]any {
		return map[string]any{"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}, "timestamp": float64(1)}}
	}
	messages := func(text string) map[string]any {
		return map[string]any{"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}, "timestamp": float64(1)}}}
	}
	return []eventEditCase{
		{event: "tool_result", edit: "content-assign", assigns: true, seen: edited(inPlaceEdited, "orig", false), caller: orig},
		{event: "tool_result", edit: "details-assign", assigns: true, seen: edited("orig", inPlaceEdited, false), caller: orig},
		{event: "tool_result", edit: "iserror-assign", assigns: true, seen: edited("orig", "orig", true), caller: orig},
		{event: "tool_result", edit: "content-deep", seen: edited(inPlaceEdited, "orig", false), caller: edited(inPlaceEdited, "orig", false)},
		{event: "tool_result", edit: "details-deep", seen: edited("orig", inPlaceEdited, false), caller: edited("orig", inPlaceEdited, false)},
		{event: "tool_result", edit: "content-assign", fail: true, assigns: true, seen: edited(inPlaceEdited, "orig", false), caller: orig},
		{event: "tool_result", edit: "content-deep", fail: true, seen: edited(inPlaceEdited, "orig", false), caller: edited(inPlaceEdited, "orig", false)},
		{event: "message_end", edit: "deep", seen: message(inPlaceEdited), caller: message(inPlaceEdited)},
		{event: "message_end", edit: "content-assign", seen: message(inPlaceEdited), caller: message(inPlaceEdited)},
		{event: "message_end", edit: "message-assign", assigns: true, seen: message("orig"), caller: message("orig")},
		{event: "message_end", edit: "deep", fail: true, seen: message(inPlaceEdited), caller: message(inPlaceEdited)},
		{event: "context", edit: "deep", seen: messages(inPlaceEdited), caller: messages(inPlaceEdited)},
		{event: "context", edit: "deep", fail: true, seen: messages(inPlaceEdited), caller: messages(inPlaceEdited)},
	}
}

func (c eventEditCase) name() string {
	name := c.event + "/" + c.edit
	if c.fail {
		name += "/fail"
	}
	return name
}

// runEventEditRows pins what Pi does with an edit a handler of the event (tool_result, message_end or context) makes in place to the event it is
// handed, from a handler that returns nothing and from one that throws after the edit. Pi hands every handler of a tool_result chain the one currentEvent
// (the next handler sees each edit), returns an edit to the caller only when it was made to an object the caller handed over, and hands every message_end
// handler the one message (the edit is the message the entry and the next request hold), and every context handler the one list of one set of message
// objects. Each handler below edits the event with the value `other-event-edit`, which only these rows use, and a second handler reports what it sees. An SDK
// that does not send the edit back loses it from the caller's result and from the next handler's event.
func runEventEditRows(t *testing.T, event string) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			for _, c := range eventEditCases() {
				if c.event != event {
					continue
				}
				if c.assigns && strings.Contains(tc.name, "rust") {
					// Rust's handler owns the event as a value with no shared object: assigning data["content"] is how it edits the content in place.
					continue
				}
				t.Run(c.name(), func(t *testing.T) {
					h := tc.make(t)
					t.Cleanup(func() {
						if h.cleanup != nil {
							h.cleanup()
						}
						if h.host != nil {
							h.host.Shutdown("test done")
						}
					})
					args := c.event + " " + c.edit
					if c.fail {
						args += " fail"
					}
					runConformanceCommandArgs(t, h, "event_edit_probe", args)
					fields := []string{"content", "details", "isError"}
					switch c.event {
					case "message_end":
						fields = []string{"message"}
					case "context":
						fields = []string{"messages"}
					}
					for _, field := range fields {
						runConformanceCommandArgs(t, h, "event_field_probe", c.event+" "+field)
					}
					ctx := context.Background()
					var got map[string]any
					switch c.event {
					case "tool_result":
						event := extension.CustomToolResultEvent{
							Type: "tool_result", ToolCallID: "c1", ToolName: "probe", Input: map[string]any{},
							Content: []any{map[string]any{"type": "text", "text": "orig"}}, Details: json.RawMessage(`{"k":"orig"}`),
						}
						result, err := h.runner.EmitToolResult(ctx, event)
						if err != nil {
							t.Fatal(err)
						}
						// The caller reads the result's members, else its own event's.
						got = decodeAny(t, map[string]any{"content": event.Content, "details": event.Details, "isError": event.IsError}).(map[string]any)
						if result != nil {
							got = decodeAny(t, map[string]any{"content": result.Content, "details": result.Details, "isError": result.IsError != nil && *result.IsError}).(map[string]any)
						}
					case "message_end":
						message := agentMessageFromJSON(t, `{"role":"user","content":[{"type":"text","text":"orig"}],"timestamp":1}`)
						replacement, err := h.runner.EmitMessageEnd(ctx, extension.MessageEndEvent{Type: "message_end", Message: message})
						if err != nil {
							t.Fatal(err)
						}
						if replacement != nil {
							message = *replacement
						}
						got = map[string]any{"message": decodeAny(t, message)}
					case "context":
						messages := []extension.AgentMessage{agentMessageFromJSON(t, `{"role":"user","content":[{"type":"text","text":"orig"}],"timestamp":1}`)}
						returned, err := h.runner.EmitContext(ctx, messages)
						if err != nil {
							t.Fatal(err)
						}
						got = map[string]any{"messages": decodeAny(t, returned)}
					}
					if !reflect.DeepEqual(got, c.caller) {
						t.Errorf("what the caller is left with\n got %v\nwant %v", got, c.caller)
					}
					seen := map[string]any{}
					for _, field := range fields {
						seen[field] = reportedField(t, h, c.event, field)
					}
					if !reflect.DeepEqual(seen, c.seen) {
						t.Errorf("what the next handler of the chain sees\n got %v\nwant %v", seen, c.seen)
					}
				})
			}
		})
	}
}

// decodeAny is value as the JSON it marshals to, decoded.
func decodeAny(t *testing.T, value any) any {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(mustMarshal(t, value), &decoded); err != nil {
		t.Fatalf("%s: %v", mustMarshal(t, value), err)
	}
	return decoded
}

// reportedField is the JSON the event_field_probe handler of the event reported for the field, decoded; nil when it reported none.
func reportedField(t *testing.T, h *harness, event, field string) any {
	t.Helper()
	prefix := "event_field:" + event + "." + field + "="
	var reported any
	for _, line := range h.ui.Recorded() {
		rest, ok := strings.CutPrefix(line, prefix)
		if !ok {
			continue
		}
		rest = strings.TrimSuffix(rest, ":info")
		if rest == "absent" {
			return nil
		}
		if err := json.Unmarshal([]byte(rest), &reported); err != nil {
			t.Fatalf("%s: %v", rest, err)
		}
	}
	return reported
}

// TestInPlaceEventEditsMatchPi runs Pi's own ExtensionRunner (the compiled runner.ts the Node runtime ships) over extensions that make the edits the probe
// handlers make, and requires what it leaves the caller and the next handler to be what the rows above require of every SDK: the oracle of the rows.
func TestInPlaceEventEditsMatchPi(t *testing.T) {
	t.Parallel()
	runner, err := filepath.Abs("../../coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-coding-agent/core/extensions/runner.js")
	if err != nil {
		t.Fatal(err)
	}
	const body = `
const { ExtensionRunner } = await import(input.runner);
const edited = "other-event-edit";
const text = () => [{ type: "text", text: edited }];
const handlerFor = (event, edit, fail) => async (e) => {
	switch (event + "/" + edit) {
	case "tool_result/content-assign": e.content = text(); break;
	case "tool_result/details-assign": e.details = { k: edited }; break;
	case "tool_result/iserror-assign": e.isError = true; break;
	case "tool_result/content-deep": e.content[0].text = edited; break;
	case "tool_result/details-deep": e.details.k = edited; break;
	case "message_end/deep": e.message.content[0].text = edited; break;
	case "message_end/content-assign": e.message.content = text(); break;
	case "message_end/message-assign": e.message = { role: "user", content: text(), timestamp: 1 }; break;
	case "context/deep": e.messages[0].content[0].text = edited; break;
	}
	if (fail) throw new Error("failed after the edit");
};
const out = {};
for (const c of input.cases) {
	const seen = {};
	const observe = async (e) => {
		if (c.event === "tool_result") Object.assign(seen, { content: e.content, details: e.details, isError: e.isError });
		else if (c.event === "message_end") seen.message = e.message;
		else seen.messages = e.messages;
	};
	const pi = new ExtensionRunner([{ path: "/probe.mjs", handlers: new Map([[c.event, [handlerFor(c.event, c.edit, c.fail), observe]]]) }], {}, "/", undefined, undefined);
	pi.onError?.(() => {});
	let caller;
	if (c.event === "tool_result") {
		const event = { type: "tool_result", toolName: "probe", toolCallId: "c1", input: {}, content: [{ type: "text", text: "orig" }], details: { k: "orig" }, isError: false };
		const result = await pi.emitToolResult(event);
		const view = result ?? event;
		caller = { content: view.content, details: view.details, isError: view.isError };
	} else if (c.event === "message_end") {
		const event = { type: "message_end", message: { role: "user", content: [{ type: "text", text: "orig" }], timestamp: 1 } };
		const result = await pi.emitMessageEnd(event);
		caller = { message: result ?? event.message };
	} else {
		caller = { messages: await pi.emitContext([{ role: "user", content: [{ type: "text", text: "orig" }], timestamp: 1 }]) };
	}
	out[c.name] = { seen: JSON.parse(JSON.stringify(seen)), caller: JSON.parse(JSON.stringify(caller)) };
}
return out;
`
	cases := eventEditCases()
	var wire []map[string]any
	for _, c := range cases {
		wire = append(wire, map[string]any{"name": c.name(), "event": c.event, "edit": c.edit, "fail": c.fail})
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
		if !reflect.DeepEqual(got[c.name()].Seen, c.seen) {
			t.Errorf("%s: what Pi's next handler sees\n got %v\nwant %v", c.name(), got[c.name()].Seen, c.seen)
		}
		if !reflect.DeepEqual(got[c.name()].Caller, c.caller) {
			t.Errorf("%s: what Pi leaves its caller\n got %v\nwant %v", c.name(), got[c.name()].Caller, c.caller)
		}
	}
}

// TestInPlaceEditsOutsideMessageEndMatchPi runs Pi's own ExtensionRunner over a handler of message_start, turn_end and agent_end that edits the message in place,
// and requires the event the caller holds to carry the edit: the behavior PiG matches for the messages of these events in every SDK (TestInPlaceMessageEventEditsMatchPi).
func TestInPlaceEditsOutsideMessageEndMatchPi(t *testing.T) {
	t.Parallel()
	runner, err := filepath.Abs("../../coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-coding-agent/core/extensions/runner.js")
	if err != nil {
		t.Fatal(err)
	}
	const body = `
const { ExtensionRunner } = await import(input.runner);
const block = () => ({ role: "assistant", content: [{ type: "text", text: "orig" }] });
const events = {
	message_start: () => ({ type: "message_start", message: block() }),
	turn_end: () => ({ type: "turn_end", turnIndex: 0, message: block(), toolResults: [] }),
	agent_end: () => ({ type: "agent_end", messages: [block()] }),
};
const out = {};
for (const [name, make] of Object.entries(events)) {
	const event = make();
	const handler = async (e) => { (e.message ?? e.messages[0]).content[0].text = "other-event-edit"; };
	const pi = new ExtensionRunner([{ path: "/probe.mjs", handlers: new Map([[name, [handler]]]) }], {}, "/", undefined, undefined);
	await pi.emit(event);
	out[name] = (event.message ?? event.messages[0]).content[0].text;
}
return out;
`
	var got map[string]string
	out := testenv.PiEvalJSON(t, body, map[string]any{"runner": "file://" + runner})
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"message_start", "turn_end", "agent_end"} {
		if got[event] != inPlaceEdited {
			t.Errorf("%s: the message the caller holds after Pi's handler edited it reads %q, want %q", event, got[event], inPlaceEdited)
		}
	}
}

// newOpenAIProbeProvider is the OpenAI provider over a recording server.
func newOpenAIProbeProvider(baseURL string) ai.Provider {
	return ai.NewOpenAIProvider(ai.OpenAIConfig{ProviderID: "test", Model: "probe", APIKey: "fixture-key", BaseURL: baseURL})
}
