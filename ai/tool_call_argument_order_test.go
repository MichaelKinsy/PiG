package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// Pi keeps a tool call's arguments as the JavaScript object JSON.parse built from the model's text. An object enumerates its
// members in insertion order, integer-like keys first and ascending, so JSON.stringify of the stored arguments writes the
// model's order. The session file, the provider request and the tool all see that text.
// upstream: .upstream/v0.99.1/packages/ai/src/utils/json-parse.ts (parseStreamingJson), utils/validation.ts:317-339
// (validateToolArguments returns structuredClone of the arguments), coding-agent/src/core/session-manager.ts (JSON.stringify of the entry).
const orderedArgumentsJSON = `{"zeta":"1","alpha":{"yy":2,"bb":[{"qq":1,"aa":2}]},"mid":true}`

func TestToolCallJSONKeepsArgumentMemberOrder(t *testing.T) {
	for _, tc := range []struct{ name, arguments, want string }{
		{"nested objects and arrays", orderedArgumentsJSON, orderedArgumentsJSON},
		{"integer-like keys enumerate first and ascending", `{"b":1,"10":2,"a":3,"2":4}`, `{"2":4,"10":2,"b":1,"a":3}`},
		{"a repeated key keeps its first position and its last value", `{"z":1,"a":2,"z":3}`, `{"z":3,"a":2}`},
		{"already sorted", `{"a":1,"b":{"c":2,"d":3}}`, `{"a":1,"b":{"c":2,"d":3}}`},
		{"empty", `{}`, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := `{"type":"toolCall","id":"c1","name":"t","arguments":` + tc.arguments + `}`
			var call ToolCall
			if err := json.Unmarshal([]byte(wire), &call); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(call)
			if err != nil {
				t.Fatal(err)
			}
			if want := `{"type":"toolCall","id":"c1","name":"t","arguments":` + tc.want + `}`; string(encoded) != want {
				t.Errorf("encoded\n%s\nwant\n%s", encoded, want)
			}
			block, err := UnmarshalContentBlock([]byte(wire))
			if err != nil {
				t.Fatal(err)
			}
			if arguments, err := block.(ToolCall).ArgumentsJSON(); err != nil || string(arguments) != tc.want {
				t.Errorf("ArgumentsJSON = %s, %v; want %s", arguments, err, tc.want)
			}
		})
	}
}

// A message clone and an assistant message round trip carry the order, because the transcript copies a tool call by value.
func TestAssistantMessageToolCallKeepsArgumentOrderThroughCloneAndJSON(t *testing.T) {
	var call ToolCall
	call.ID, call.Name = "c1", "t"
	call.SetStreamingArguments(orderedArgumentsJSON)
	message := AssistantMessage{Content: []AssistantContentBlock{call}}
	cloned := cloneAssistantMessage(message)
	arguments, err := cloned.Content[0].(ToolCall).ArgumentsJSON()
	if err != nil || string(arguments) != orderedArgumentsJSON {
		t.Fatalf("cloned ArgumentsJSON = %s, %v; want %s", arguments, err, orderedArgumentsJSON)
	}
	encoded, err := json.Marshal(cloned.Content[0])
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"type":"toolCall","id":"c1","name":"t","arguments":` + orderedArgumentsJSON + `}`; string(encoded) != want {
		t.Fatalf("encoded\n%s\nwant\n%s", encoded, want)
	}
}

// parseStreamingJson parses a complete text strictly and an unfinished one by its prefix. Both keep the text's member order.
func TestSetStreamingArgumentsKeepsMemberOrder(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"complete", orderedArgumentsJSON, orderedArgumentsJSON},
		{"unfinished string", `{"zeta":"1","alpha":"a","mid":"par`, `{"zeta":"1","alpha":"a","mid":"par"}`},
		{"unfinished nested object", `{"zeta":1,"alpha":{"yy":2,"bb":3,"aa`, `{"zeta":1,"alpha":{"yy":2,"bb":3}}`},
		{"unfinished array of objects", `{"zeta":1,"edits":[{"oldText":"a","newText":"b"},{"oldText":"c","newT`, `{"zeta":1,"edits":[{"oldText":"a","newText":"b"},{"oldText":"c"}]}`},
		{"empty", ``, `{}`},
		{"not an object", `[1,2]`, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var call ToolCall
			call.SetStreamingArguments(tc.input)
			arguments, err := call.ArgumentsJSON()
			if err != nil || string(arguments) != tc.want {
				t.Fatalf("ArgumentsJSON = %s, %v; want %s", arguments, err, tc.want)
			}
		})
	}
}

// Code builds and edits Arguments as a map. The model's order applies to the members the call still has; a member added
// afterwards follows them and the others keep their place, as a JavaScript assignment appends a new property.
func TestToolCallArgumentsJSONAfterMutation(t *testing.T) {
	var call ToolCall
	call.SetStreamingArguments(`{"zeta":"1","alpha":"a","mid":"m"}`)
	delete(call.Arguments, "alpha")
	call.Arguments["beta"] = "b"
	call.Arguments["zeta"] = "changed"
	arguments, err := call.ArgumentsJSON()
	if want := `{"zeta":"changed","mid":"m","beta":"b"}`; err != nil || string(arguments) != want {
		t.Fatalf("ArgumentsJSON = %s, %v; want %s", arguments, err, want)
	}
	if arguments, err := (ToolCall{Arguments: JsonObject{"b": 1, "a": 2}}).ArgumentsJSON(); err != nil || string(arguments) != `{"a":2,"b":1}` {
		t.Fatalf("a call built from a map = %s, %v; want sorted members", arguments, err)
	}
	if arguments, err := (ToolCall{}).ArgumentsJSON(); err != nil || string(arguments) != `{}` {
		t.Fatalf("a call without arguments = %s, %v; want {}", arguments, err)
	}
}

func TestMarshalJSONInSourceOrder(t *testing.T) {
	var value any
	if err := json.Unmarshal([]byte(`{"a":1,"z":{"y":[{"q":1,"b":2}],"x":3}}`), &value); err != nil {
		t.Fatal(err)
	}
	const source = `{"z":{"x":3,"y":[{"q":1,"b":2}]},"a":1}`
	encoded, err := MarshalJSONInSourceOrder(value, []byte(source))
	if err != nil || string(encoded) != source {
		t.Fatalf("encoded = %s, %v; want %s", encoded, err, source)
	}
	if encoded, err := MarshalJSONInSourceOrder(value, []byte(`not json`)); err != nil || string(encoded) != `{"a":1,"z":{"x":3,"y":[{"b":2,"q":1}]}}` {
		t.Fatalf("an unreadable source = %s, %v; want sorted members", encoded, err)
	}
}

// The provider stream builder rebuilds the arguments on every delta and at the end of the call. Each snapshot keeps the order.
func TestStreamBuilderToolCallKeepsArgumentOrder(t *testing.T) {
	builder := newAssistantStreamBuilder(t.Context(), "openai-completions", "p", "m")
	builder.toolCallDelta(streamToolCallDelta{index: 0, id: "c1", name: "t", argumentsDelta: `{"zeta":"1",`})
	builder.toolCallDelta(streamToolCallDelta{index: 0, argumentsDelta: `"alpha":"a"}`})
	partial := builder.partial.Content[0].(ToolCall)
	if arguments, err := partial.ArgumentsJSON(); err != nil || string(arguments) != `{"zeta":"1","alpha":"a"}` {
		t.Fatalf("delta snapshot = %s, %v", arguments, err)
	}
	builder.endToolCall(0)
	final := builder.partial.Content[0].(ToolCall)
	if arguments, err := final.ArgumentsJSON(); err != nil || string(arguments) != `{"zeta":"1","alpha":"a"}` {
		t.Fatalf("final call = %s, %v", arguments, err)
	}
	encoded, err := json.Marshal(final)
	if err != nil || string(encoded) != `{"type":"toolCall","id":"c1","name":"t","arguments":{"zeta":"1","alpha":"a"}}` {
		t.Fatalf("encoded = %s, %v", encoded, err)
	}
}

// A provider request replays a past tool call as JSON.stringify(toolCall.arguments), the model's member order, in the
// arguments string (OpenAI, Mistral) and in the input object (Anthropic, Google).
// upstream: .upstream/v0.99.1/packages/ai/src/providers/openai-completions.ts, openai-responses-shared.ts, mistral.ts,
// anthropic.ts, google-shared.ts (convertMessages: JSON.stringify(toolCall.arguments) / input: toolCall.arguments).
func TestProviderRequestsReplayToolCallArgumentsInModelOrder(t *testing.T) {
	var call ToolCall
	call.ID, call.Name = "call_1", "probe"
	call.SetStreamingArguments(orderedArgumentsJSON)
	assistant := func(api API, provider, model string) []Message {
		return []Message{
			UserMessage{Content: UserText("go")},
			AssistantMessage{API: api, Provider: provider, Model: model, StopReason: StopReasonToolUse, Content: []AssistantContentBlock{call}},
			ToolResultMessage{ToolCallID: "call_1", ToolName: "probe", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}},
		}
	}
	// argumentsOf finds the replayed arguments in the encoded request fragment, as a JSON value or as a string holding one.
	check := func(t *testing.T, name string, payload any) {
		t.Helper()
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		text := string(encoded)
		quoted, _ := json.Marshal(orderedArgumentsJSON)
		if !strings.Contains(text, orderedArgumentsJSON) && !strings.Contains(text, string(quoted)) {
			t.Errorf("%s request does not carry the arguments in the model's order %s:\n%s", name, orderedArgumentsJSON, text)
		}
	}

	t.Run("openai completions", func(t *testing.T) {
		out, err := convertMessages(assistant(APIOpenAICompletions, "openai", "gpt-test"), false, nil)
		if err != nil {
			t.Fatal(err)
		}
		check(t, "openai completions", out)
	})
	t.Run("openai responses", func(t *testing.T) {
		provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai", Model: "gpt-test"}}
		out, err := provider.convertMessages(assistant(APIOpenAIResponses, "openai", "gpt-test"), nil)
		if err != nil {
			t.Fatal(err)
		}
		check(t, "openai responses", out)
	})
	t.Run("mistral", func(t *testing.T) {
		out := (&mistralProvider{}).convertMessages(assistant(APIMistralConversations, "mistral", "m"), false)
		check(t, "mistral", out)
	})
	t.Run("anthropic", func(t *testing.T) {
		out, err := anthropicParams{}.convertMessages(assistant(APIAnthropicMessages, "anthropic", "claude-test"), false)
		if err != nil {
			t.Fatal(err)
		}
		check(t, "anthropic", out)
	})
	t.Run("google", func(t *testing.T) {
		out := geminiConvertMessages(assistant(APIGoogleGenerativeAI, "google", "gemini-test"), "google", "gemini-test", false)
		check(t, "google", out)
	})
}

func TestSetArgumentsJSONKeepsMemberOrderAndRejectsANonObject(t *testing.T) {
	var call ToolCall
	if err := call.SetArgumentsJSON([]byte(orderedArgumentsJSON)); err != nil {
		t.Fatal(err)
	}
	if arguments, err := call.ArgumentsJSON(); err != nil || string(arguments) != orderedArgumentsJSON {
		t.Fatalf("ArgumentsJSON = %s, %v; want %s", arguments, err, orderedArgumentsJSON)
	}
	for _, input := range []string{`[1]`, `"x"`, `{"a":`, ``} {
		before, _ := call.ArgumentsJSON()
		if err := call.SetArgumentsJSON([]byte(input)); err == nil {
			t.Errorf("SetArgumentsJSON(%q) = nil, want an error", input)
		}
		if after, _ := call.ArgumentsJSON(); string(after) != string(before) {
			t.Errorf("SetArgumentsJSON(%q) changed the call to %s", input, after)
		}
	}
}

// JSON.stringify(JSON.parse(text)) writes integer-like keys first and ascending even when every other key is already sorted,
// so a call whose only difference from sorted order is its integer-like keys must not fall back to byte-sorted encoding.
// A repeated key keeps its first position and takes its last value, and that value's own member order replaces the order of
// the value it overwrote at every depth. Expected texts are node v24 output of JSON.stringify(JSON.parse(arguments)); the
// unfinished case follows partial-json's parseObj, which assigns obj[key] = value as JSON.parse does.
// upstream: .upstream/v0.99.2/packages/ai/src/utils/json-parse.ts (parseStreamingJson: JSON.parse, then partial-json parse).
func TestToolCallArgumentOrderIntegerKeysAndReplacedValues(t *testing.T) {
	for _, tc := range []struct{ name, arguments, want string }{
		{"integer-like keys only", `{"10":1,"9":2}`, `{"9":2,"10":1}`},
		{"integer-like key before a key that sorts below digits", `{"1":1,"$a":2}`, `{"1":1,"$a":2}`},
		{"nested integer-like keys in a sorted parent", `{"a":{"10":1,"9":2},"b":1}`, `{"a":{"9":2,"10":1},"b":1}`},
		{"a repeated key's sorted value replaces an unsorted one", `{"a":{"z":1,"b":2},"a":{"b":1,"z":2}}`, `{"a":{"b":1,"z":2}}`},
		{"a repeated key replaces the order below it", `{"a":{"x":{"z":1,"b":1}},"a":{"x":{"b":1,"z":1}}}`, `{"a":{"x":{"b":1,"z":1}}}`},
		{"a repeated key beside an unsorted member", `{"z":1,"a":{"z":1,"b":2},"a":{"b":1,"z":2}}`, `{"z":1,"a":{"b":1,"z":2}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var decoded ToolCall
			if err := json.Unmarshal([]byte(`{"type":"toolCall","id":"c1","name":"t","arguments":`+tc.arguments+`}`), &decoded); err != nil {
				t.Fatal(err)
			}
			if encoded, err := json.Marshal(decoded); err != nil || string(encoded) != `{"type":"toolCall","id":"c1","name":"t","arguments":`+tc.want+`}` {
				t.Errorf("decoded call = %s, %v; want arguments %s", encoded, err, tc.want)
			}
			var streamed ToolCall
			streamed.SetStreamingArguments(tc.arguments)
			if arguments, err := streamed.ArgumentsJSON(); err != nil || string(arguments) != tc.want {
				t.Errorf("streamed ArgumentsJSON = %s, %v; want %s", arguments, err, tc.want)
			}
			// Dropping the final "}" sends the text through the partial parser instead of the strict one.
			var unfinished ToolCall
			unfinished.SetStreamingArguments(tc.arguments[:len(tc.arguments)-1])
			if arguments, err := unfinished.ArgumentsJSON(); err != nil || string(arguments) != tc.want {
				t.Errorf("unfinished ArgumentsJSON = %s, %v; want %s", arguments, err, tc.want)
			}
		})
	}
}

// An unfinished repeated key keeps the earlier value and its order until a new value is read. Expected texts are
// JSON.stringify of partial-json 0.1.7 parse(input), the parser Pi's parseStreamingJson falls back to.
func TestSetStreamingArgumentsRepeatedKeyWithAnUnfinishedValue(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"a":{"z":1,"b":2},"a":`, `{"a":{"z":1,"b":2}}`},
		{`{"a":{"z":1,"b":2},"a":{"b":1,"z`, `{"a":{"b":1}}`},
	} {
		var call ToolCall
		call.SetStreamingArguments(tc.input)
		if arguments, err := call.ArgumentsJSON(); err != nil || string(arguments) != tc.want {
			t.Errorf("SetStreamingArguments(%s): ArgumentsJSON = %s, %v; want %s", tc.input, arguments, err, tc.want)
		}
	}
}

// Google replays a past tool call as functionCall.args = block.arguments ?? {}, so a call with no arguments still sends an
// empty args object.
// upstream: .upstream/v0.99.2/packages/ai/src/api/google-shared.ts:265-272 (convertMessages).
func TestGoogleReplaysAnEmptyToolCallArgumentsObject(t *testing.T) {
	for name, arguments := range map[string]JsonObject{"empty": {}, "nil": nil} {
		messages := []Message{
			UserMessage{Content: UserText("go")},
			AssistantMessage{API: APIGoogleGenerativeAI, Provider: "google", Model: "gemini-test", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: "call_1", Name: "probe", Arguments: arguments}}},
			ToolResultMessage{ToolCallID: "call_1", ToolName: "probe", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}},
		}
		encoded, err := json.Marshal(geminiConvertMessages(messages, "google", "gemini-test", false))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), `"functionCall":{"name":"probe","args":{}}`) {
			t.Errorf("%s arguments: request %s, want functionCall.args {}", name, encoded)
		}
	}
}

// Google delivers a function call whole, and Pi builds the tool call with its arguments before it pushes toolcall_start, so
// the start, delta and end snapshots all carry functionCall.args in the model's member order.
// upstream: .upstream/v0.99.2/packages/ai/src/api/google-generative-ai.ts:203-217.
func TestGoogleStreamToolCallEventsKeepArgumentOrder(t *testing.T) {
	const arguments = `{"zeta":"1","alpha":{"yy":2,"bb":3}}`
	sse := `data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"probe","args":` + arguments + `}}]},"finishReason":"STOP"}]}

`
	seen := map[string]bool{}
	for _, event := range runGoogleSSE(t, sse) {
		var kind string
		var partial *AssistantMessage
		switch event := event.(type) {
		case ToolCallStartEvent:
			kind, partial = "start", event.Partial
		case ToolCallDeltaEvent:
			kind, partial = "delta", event.Partial
			if event.Delta != arguments {
				t.Errorf("toolcall_delta delta = %s, want %s", event.Delta, arguments)
			}
		case ToolCallEndEvent:
			kind, partial = "end", event.Partial
			if got, err := event.ToolCall.ArgumentsJSON(); err != nil || string(got) != arguments {
				t.Errorf("toolcall_end toolCall arguments = %s, %v; want %s", got, err, arguments)
			}
		default:
			continue
		}
		seen[kind] = true
		if got, err := partial.Content[0].(ToolCall).ArgumentsJSON(); err != nil || string(got) != arguments {
			t.Errorf("toolcall_%s partial arguments = %s, %v; want %s", kind, got, err, arguments)
		}
	}
	if !seen["start"] || !seen["delta"] || !seen["end"] {
		t.Fatalf("events seen = %v, want start, delta and end", seen)
	}
}

// ToolSchema shares the order reader, so a schema object whose only difference from byte-sorted order is its integer-like keys
// also round-trips in JSON.stringify order.
func TestToolSchemaRoundTripsIntegerLikeKeysInJavaScriptOrder(t *testing.T) {
	const wire = `{"name":"t","description":"d","parameters":{"properties":{"9":{"type":"string"},"10":{"type":"string"}},"type":"object"}}`
	var tool ToolSchema
	if err := json.Unmarshal([]byte(wire), &tool); err != nil {
		t.Fatal(err)
	}
	if encoded, err := json.Marshal(tool); err != nil || string(encoded) != wire {
		t.Fatalf("encoded = %s, %v; want %s", encoded, err, wire)
	}
}
