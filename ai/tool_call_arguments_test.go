package ai

import (
	"bytes"
	"math"
	"reflect"
	"testing"
)

// JSON.stringify(toolCall.arguments) is what a provider request carries back to the model: no HTML or U+2028/U+2029 escaping, integer-like keys ascending first, and a lone surrogate kept as its escape. A call whose objects are already in sorted order has no recorded member order, and must still be written that way.
func TestArgumentsJSONIsJSONStringifyWithoutARecordedOrder(t *testing.T) {
	for name, test := range map[string]struct{ in, want string }{
		"html":              {`{"k":"<a>&"}`, `{"k":"<a>&"}`},
		"line separators":   {"{\"k\":\"\u2028\u2029\"}", "{\"k\":\"\u2028\u2029\"}"},
		"lone surrogate":    {`{"k":"a\ud83db"}`, `{"k":"a\ud83db"}`},
		"integer-like keys": {`{"10":1,"2":2,"a":3}`, `{"2":2,"10":1,"a":3}`},
		"nested":            {`{"a":[{"k":"<&>"}],"b":{"2":1,"10":2}}`, `{"a":[{"k":"<&>"}],"b":{"2":1,"10":2}}`},
	} {
		var call ToolCall
		if err := call.SetArgumentsJSON([]byte(test.in)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := call.ArgumentsJSON()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != test.want {
			t.Errorf("%s: ArgumentsJSON = %s, want %s", name, got, test.want)
		}
	}
}

// A model's tool-call text that carries a lone surrogate escape reaches the arguments as JSON.parse reads it and goes back out as JSON.stringify writes it.
func TestStreamedArgumentsKeepALoneSurrogate(t *testing.T) {
	var call ToolCall
	call.SetStreamingArguments(`{"k":"a\ud83db"}`)
	if got := call.Arguments["k"]; got != "a\xed\xa0\xbdb" {
		t.Fatalf("argument = %q, want the lone surrogate kept", got)
	}
	got, err := call.ArgumentsJSON()
	if err != nil || string(got) != `{"k":"a\ud83db"}` {
		t.Fatalf("ArgumentsJSON = %s, %v", got, err)
	}
}

// The arguments string an OpenAI-compatible request replays to the model is JSON.stringify(toolCall.arguments): markup characters are not escaped, however the call was built.
func TestOpenAIRequestReplaysArgumentsAsJSONStringify(t *testing.T) {
	messages := []Message{
		AssistantMessage{API: APIOpenAICompletions, Provider: "openai", Model: "gpt-test", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: "call_1", Name: "edit", Arguments: JsonObject{"text": "<a href=\"x\">&</a>"}}}},
		ToolResultMessage{ToolCallID: "call_1", ToolName: "edit", Content: []ToolResultMessageContent{TextContent{Text: "done"}}},
	}
	converted, err := convertMessages(messages, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(converted) == 0 || len(converted[0].ToolCalls) != 1 || converted[0].ToolCalls[0].Function == nil {
		t.Fatalf("converted = %+v", converted)
	}
	if got, want := converted[0].ToolCalls[0].Function.Arguments, `{"text":"<a href=\"x\">&</a>"}`; got != want {
		t.Errorf("arguments = %s, want %s", got, want)
	}
}

func TestIsWTF8(t *testing.T) {
	for text, want := range map[string]bool{
		"": true, "ascii": true, "é🦀": true, "a\xed\xa0\xbdb": true, "\xed\xbf\xbf": true,
		"\xff": false, "a\xc3": false, "\xed\xa0": false, "\xed\xc0\x80": false, "\xf0\x28\x8c\x28": false,
	} {
		if got := isWTF8(text); got != want {
			t.Errorf("isWTF8(%q) = %t, want %t", text, got, want)
		}
	}
}

// jsonParseValue runs once per streamed tool-call delta, so a Decoder (which allocates its refill buffer on every call) is only for the out-of-range number Unmarshal refuses. The out-of-range literal must still read as JSON.parse's infinity; the allocation budget of the in-range value is in tool_call_arguments_allocs_norace_test.go.
func TestJSONParseValueReadsOutOfRangeNumbersAsInfinity(t *testing.T) {
	if value, ok := jsonParseValue(`[1e999, -1e999]`); !ok || !reflect.DeepEqual(value, []any{math.Inf(1), math.Inf(-1)}) {
		t.Errorf("an out-of-range number = %v, %v, want the infinities JSON.parse gives", value, ok)
	}
}

// Pi validates structuredClone(toolCall.arguments) (validation.ts:317-339), so a lone surrogate the model sent reaches the tool as one UTF-16 unit. The agent validates through ValidateToolArgumentsJSON and ValidateToolArgumentsSchema (agent/tool_execution.go); neither may hand the tool U+FFFD.
func TestValidatedArgumentsKeepALoneSurrogate(t *testing.T) {
	const lone = "a\xed\xa0\xbdb"
	schema := map[string]any{"type": "object", "properties": map[string]any{"k": map[string]any{"type": "string"}}}
	args := []byte(`{"k":"a\ud83db"}`)
	for name, validate := range map[string]func() ([]byte, error){
		"json schema": func() ([]byte, error) {
			return ValidateToolArgumentsJSON("x", []byte(`{"type":"object","properties":{"k":{"type":"string"}}}`), args)
		},
		"map schema": func() ([]byte, error) { return ValidateToolArgumentsSchema("x", schema, args) },
	} {
		got, err := validate()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(got, args) {
			t.Errorf("%s validated %s, want %s", name, got, args)
		}
	}
	var call ToolCall
	if err := call.SetArgumentsJSON(args); err != nil {
		t.Fatal(err)
	}
	validated, err := ValidateToolArguments(ToolSchema{Name: "x", Parameters: schema}, call)
	if err != nil {
		t.Fatal(err)
	}
	if validated["k"] != lone {
		t.Errorf("ValidateToolArguments k = %q, want %q", validated["k"], lone)
	}
}
