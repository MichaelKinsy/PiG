package ai

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Pi packages/ai/src/api/constrained-sampling.ts (getJsonSchemaToolParameters, prefersJsonSchemaStrictSampling,
// resolveJsonSchemaStrictSampling) and the callers that serialize their result into a request:
// openai-completions.ts convertTools (strict: strict ?? false when supportsStrictMode) and
// openai-responses-shared.ts convertResponsesTools (strict set when supportsStrictMode).

func strictToolFromJSON(t *testing.T, raw string) ToolSchema {
	t.Helper()
	var tool ToolSchema
	if err := json.Unmarshal([]byte(raw), &tool); err != nil {
		t.Fatalf("tool %s: %v", raw, err)
	}
	return tool
}

const (
	strictLookupParams = `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"number"}},"required":["a"]}`
	// makeJsonSchemaNodeStrict: every property is required, an optional one becomes anyOf [property, null], additionalProperties false is added last.
	strictLookupStrictParams = `{"type":"object","properties":{"a":{"type":"string"},"b":{"anyOf":[{"type":"number"},{"type":"null"}]}},"required":["a","b"],"additionalProperties":false}`
	refLookupParams          = `{"type":"object","properties":{"a":{"$ref":"#/x"}},"required":["a"]}`
)

func strictLookupTool(t *testing.T, strict string, parameters string) ToolSchema {
	t.Helper()
	return strictToolFromJSON(t, `{"name":"lookup","description":"d","parameters":`+parameters+`,"constrainedSampling":{"type":"json_schema","strict":"`+strict+`"}}`)
}

func TestGetJSONSchemaToolParametersFollowsPi(t *testing.T) {
	tool := strictLookupTool(t, "prefer", strictLookupParams)
	for name, strict := range map[string]*bool{"undefined": nil, "false": new(false)} {
		got, err := GetJSONSchemaToolParameters(tool, strict)
		if err != nil {
			t.Fatalf("strict %s: %v", name, err)
		}
		encoded, err := json.Marshal(got)
		if err != nil || string(encoded) != strictLookupParams {
			t.Fatalf("strict %s must return the tool's own parameters unchanged: %s, %v", name, encoded, err)
		}
	}
	got, err := GetJSONSchemaToolParameters(tool, new(true))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil || string(encoded) != strictLookupStrictParams {
		t.Fatalf("strict true parameters:\n got %s\nwant %s (%v)", encoded, strictLookupStrictParams, err)
	}
	if original, _ := json.Marshal(tool.Parameters); string(original) == strictLookupStrictParams {
		t.Fatalf("the strict schema is a clone: the tool's own parameters were rewritten: %s", original)
	}
	for _, rejected := range []struct{ parameters, message string }{
		{`{"type":"object","properties":{"a":{"$ref":"#/x"}}}`, "$ref schemas are unsupported"},
		{`{"type":"object","properties":{"a":{"type":"array","items":[{"type":"string"}]}}}`, "tuple schemas are unsupported"},
		{`{"type":"string"}`, "root schema must have type object"},
		{`{"type":"object","additionalProperties":true}`, "schema-valued or true additionalProperties is unsupported"},
	} {
		if _, err := GetJSONSchemaToolParameters(strictLookupTool(t, "prefer", rejected.parameters), new(true)); err == nil || err.Error() != rejected.message {
			t.Fatalf("strict true over %s: error = %v, want %q", rejected.parameters, err, rejected.message)
		}
	}
}

func TestResolveJSONSchemaStrictSamplingFollowsPi(t *testing.T) {
	plain := strictToolFromJSON(t, `{"name":"lookup","description":"d","parameters":`+strictLookupParams+`}`)
	grammar := strictToolFromJSON(t, `{"name":"lookup","description":"d","parameters":`+strictLookupParams+`,"constrainedSampling":{"type":"grammar","variants":{"openai_lark":"start: x"}}}`)
	rejectMinimum := func(key string, _ any) bool { return key == "minimum" }
	withMinimum := `{"type":"object","properties":{"a":{"type":"number","minimum":1}},"required":["a"]}`
	cases := []struct {
		name     string
		tool     ToolSchema
		supports bool
		keyword  UnsupportedStrictSchemaKeywordCheck
		want     *bool
		err      string
	}{
		{"no constrained sampling", plain, true, nil, nil, ""},
		{"grammar sampling is not json schema", grammar, true, nil, nil, ""},
		{"prefer with a strict-capable schema", strictLookupTool(t, "prefer", strictLookupParams), true, nil, new(true), ""},
		{"require with a strict-capable schema", strictLookupTool(t, "require", strictLookupParams), true, nil, new(true), ""},
		{"prefer falls back when the schema is unsupported", strictLookupTool(t, "prefer", refLookupParams), true, nil, nil, ""},
		{"require throws for an unsupported schema", strictLookupTool(t, "require", refLookupParams), true, nil, nil, `Tool "lookup" requires JSON-schema constrained sampling, but $ref schemas are unsupported.`},
		{"prefer without strict support", strictLookupTool(t, "prefer", strictLookupParams), false, nil, nil, ""},
		{"require without strict support", strictLookupTool(t, "require", strictLookupParams), false, nil, nil, `Tool "lookup" requires JSON-schema constrained sampling, but strict tools are unsupported.`},
		{"provider keyword check rejects, prefer falls back", strictLookupTool(t, "prefer", withMinimum), true, rejectMinimum, nil, ""},
		{"provider keyword check rejects, require throws", strictLookupTool(t, "require", withMinimum), true, rejectMinimum, nil, `Tool "lookup" requires JSON-schema constrained sampling, but minimum: 1 is unsupported.`},
		{"without the keyword check the keyword is accepted", strictLookupTool(t, "prefer", withMinimum), true, nil, new(true), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveJSONSchemaStrictSampling(tc.tool, tc.supports, tc.keyword)
			if tc.err != "" {
				if err == nil || err.Error() != tc.err {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("result = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestPrefersJSONSchemaStrictSamplingFollowsPi(t *testing.T) {
	for name, tc := range map[string]struct {
		config string
		want   bool
	}{
		"prefer":           {`,"constrainedSampling":{"type":"json_schema","strict":"prefer"}`, true},
		"require":          {`,"constrainedSampling":{"type":"json_schema","strict":"require"}`, false},
		"json schema only": {`,"constrainedSampling":{"type":"json_schema"}`, false},
		"grammar":          {`,"constrainedSampling":{"type":"grammar","variants":{"openai_lark":"start: x"}}`, false},
		"none":             {``, false},
	} {
		tool := strictToolFromJSON(t, `{"name":"lookup","description":"d","parameters":`+strictLookupParams+tc.config+`}`)
		if got := PrefersJSONSchemaStrictSampling(tool); got != tc.want {
			t.Fatalf("%s: PrefersJSONSchemaStrictSampling = %t, want %t", name, got, tc.want)
		}
	}
}

// The three functions decide what both OpenAI-compatible request builders serialize. The expected bytes are what Pi's convertTools
// (openai-completions.ts:1494-1505) and convertResponsesTools (openai-responses-shared.ts:460-472) build for the same tools.
func TestStrictSamplingSerializedInOpenAICompletionsAndResponsesToolsLikePi(t *testing.T) {
	cases := []struct {
		name                string
		tool                ToolSchema
		completions         string
		completionsNoStrict string
		responses           string
		responsesNoStrict   string
	}{
		{
			name:                "prefer with a strict-capable schema",
			tool:                strictLookupTool(t, "prefer", strictLookupParams),
			completions:         `{"type":"function","function":{"name":"lookup","description":"d","parameters":` + strictLookupStrictParams + `,"strict":true}}`,
			completionsNoStrict: `{"type":"function","function":{"name":"lookup","description":"d","parameters":` + strictLookupParams + `}}`,
			responses:           `{"type":"function","name":"lookup","description":"d","parameters":` + strictLookupStrictParams + `,"strict":true}`,
			responsesNoStrict:   `{"type":"function","name":"lookup","description":"d","parameters":` + strictLookupParams + `}`,
		},
		{
			name:                "prefer falls back to the tool's own schema with strict false",
			tool:                strictLookupTool(t, "prefer", refLookupParams),
			completions:         `{"type":"function","function":{"name":"lookup","description":"d","parameters":` + refLookupParams + `,"strict":false}}`,
			completionsNoStrict: `{"type":"function","function":{"name":"lookup","description":"d","parameters":` + refLookupParams + `}}`,
			responses:           `{"type":"function","name":"lookup","description":"d","parameters":` + refLookupParams + `,"strict":false}`,
			responsesNoStrict:   `{"type":"function","name":"lookup","description":"d","parameters":` + refLookupParams + `}`,
		},
		{
			name:                "plain tool",
			tool:                strictToolFromJSON(t, `{"name":"lookup","description":"d","parameters":`+strictLookupParams+`}`),
			completions:         `{"type":"function","function":{"name":"lookup","description":"d","parameters":` + strictLookupParams + `,"strict":false}}`,
			completionsNoStrict: `{"type":"function","function":{"name":"lookup","description":"d","parameters":` + strictLookupParams + `}}`,
			responses:           `{"type":"function","name":"lookup","description":"d","parameters":` + strictLookupParams + `,"strict":false}`,
			responsesNoStrict:   `{"type":"function","name":"lookup","description":"d","parameters":` + strictLookupParams + `}`,
		},
	}
	encode := func(t *testing.T, value any, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			completions := &openAIProvider{cfg: OpenAIConfig{Compat: &OpenAICompat{SupportsStrictMode: new(true)}}}
			out, err := completions.convertTools([]ToolSchema{tc.tool})
			if got := encode(t, out[0], err); got != tc.completions {
				t.Fatalf("openai-completions with strict mode:\n got %s\nwant %s", got, tc.completions)
			}
			out, err = (&openAIProvider{cfg: OpenAIConfig{Compat: &OpenAICompat{SupportsStrictMode: new(false)}}}).convertTools([]ToolSchema{tc.tool})
			if got := encode(t, out[0], err); got != tc.completionsNoStrict {
				t.Fatalf("openai-completions without strict mode:\n got %s\nwant %s", got, tc.completionsNoStrict)
			}
			responses := &openAIResponsesProvider{}
			rout, err := responses.convertTools([]ToolSchema{tc.tool}, true, false)
			if got := encode(t, rout[0], err); got != tc.responses {
				t.Fatalf("openai-responses with strict mode:\n got %s\nwant %s", got, tc.responses)
			}
			rout, err = responses.convertTools([]ToolSchema{tc.tool}, false, false)
			if got := encode(t, rout[0], err); got != tc.responsesNoStrict {
				t.Fatalf("openai-responses without strict mode:\n got %s\nwant %s", got, tc.responsesNoStrict)
			}
		})
	}
	// A required strict tool whose schema cannot be strict stops both builders with Pi's message.
	required := strictLookupTool(t, "require", refLookupParams)
	want := `Tool "lookup" requires JSON-schema constrained sampling, but $ref schemas are unsupported.`
	if _, err := (&openAIProvider{cfg: OpenAIConfig{Compat: &OpenAICompat{SupportsStrictMode: new(true)}}}).convertTools([]ToolSchema{required}); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("openai-completions require: %v", err)
	}
	if _, err := (&openAIResponsesProvider{}).convertTools([]ToolSchema{required}, true, false); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("openai-responses require: %v", err)
	}
}

// Pi openai-responses-shared.ts getResponsesToolSupport: "changes" needs mid-conversation system messages and additional_tools,
// otherwise "additions" when tool search is supported, otherwise "none".
func TestGetResponsesToolSupportFollowsPi(t *testing.T) {
	for mask := range 8 {
		options := ResponsesToolSupportOptions{SupportsMidConvoSystemMessages: mask&1 != 0, SupportsAdditionalTools: mask&2 != 0, SupportsToolSearch: mask&4 != 0}
		want := TranscriptToolSupportNone
		switch {
		case options.SupportsMidConvoSystemMessages && options.SupportsAdditionalTools:
			want = TranscriptToolSupportChanges
		case options.SupportsToolSearch:
			want = TranscriptToolSupportAdditions
		}
		if got := GetResponsesToolSupport(options); got != want {
			t.Fatalf("GetResponsesToolSupport(%+v) = %q, want %q", options, got, want)
		}
	}
	if got := (responsesMessageConverter{options: ConvertResponsesMessagesOptions{SupportsMidConvoSystemMessages: true, SupportsAdditionalTools: true}}).toolSupport(); got != "changes" {
		t.Fatalf("the converter reads the same flags: toolSupport = %q", got)
	}
	if string(TranscriptToolSupportChanges) != "changes" || string(TranscriptToolSupportAdditions) != "additions" || string(TranscriptToolSupportNone) != "none" {
		t.Fatal("the three support levels are Pi's literal strings")
	}
}

// Pi transform-messages.ts splitErrorToolResultImages.
func TestSplitErrorToolResultImagesFollowsPi(t *testing.T) {
	image := func(data string) ImageContent { return ImageContent{Data: data, MimeType: "image/png"} }
	message := func(isError bool, content ...ToolResultMessageContent) ToolResultMessage {
		return ToolResultMessage{ToolCallID: "call_1", ToolName: "shot", IsError: isError, Content: content}
	}
	if got := SplitErrorToolResultImages(message(false, TextContent{Text: "ok"}, image("a"))); got != nil {
		t.Fatalf("a successful result needs no split: %+v", got)
	}
	if got := SplitErrorToolResultImages(message(true, TextContent{Text: "boom"})); got != nil {
		t.Fatalf("an error result without images needs no split: %+v", got)
	}
	got := SplitErrorToolResultImages(message(true, TextContent{Text: "boom"}, image("a"), image("b"), TextContent{Text: "tail"}, image("c")))
	if got == nil {
		t.Fatal("an error result with images is split")
	}
	const placeholder = "(image attached after the tool results)"
	wantContent := []TextContent{{Text: "boom"}, {Text: placeholder}, {Text: "tail"}, {Text: placeholder}}
	if !reflect.DeepEqual(got.Content, wantContent) {
		t.Fatalf("content = %#v, want a run of images replaced by one placeholder: %#v", got.Content, wantContent)
	}
	if got.Caption != "Images from the error result of tool call call_1 (shot):" {
		t.Fatalf("caption = %q", got.Caption)
	}
	if !reflect.DeepEqual(got.Images, []ImageContent{image("a"), image("b"), image("c")}) {
		t.Fatalf("images = %#v", got.Images)
	}
	// replaceImagesWithPlaceholder: an image right after a text block that already is the placeholder adds no second note.
	got = SplitErrorToolResultImages(message(true, TextContent{Text: placeholder}, image("a")))
	if got == nil || !reflect.DeepEqual(got.Content, []TextContent{{Text: placeholder}}) {
		t.Fatalf("an image after the placeholder text adds no second placeholder: %#v", got)
	}
}

// Pi streaming-json.ts ToolCallArgumentsStream.finish: one full parse of the complete text, which reports malformed arguments.
func TestToolCallArgumentsStreamFinishFollowsPi(t *testing.T) {
	finish := func(stream *ToolCallArgumentsStream, pieces ...string) (JsonObject, schemaObjectOrder, string) {
		for _, piece := range pieces {
			stream.Append(piece)
		}
		return stream.Finish()
	}
	arguments, order, message := finish(NewToolCallArgumentsStream(), `{"b":`, `[1,2],"a":"x`, `y"}`)
	if message != "" || !reflect.DeepEqual(arguments, JsonObject{"a": "xy", "b": []any{float64(1), float64(2)}}) {
		t.Fatalf("Finish = %#v, %q", arguments, message)
	}
	if encoded, err := MarshalInRecordedOrder(map[string]any(arguments), order); err != nil || string(encoded) != `{"b":[1,2],"a":"xy"}` {
		t.Fatalf("Finish keeps the member order of the text: %s, %v", encoded, err)
	}

	empty, _, message := finish(NewToolCallArgumentsStream())
	if message != "" || len(empty) != 0 {
		t.Fatalf("a stream without text finishes as no arguments: %#v, %q", empty, message)
	}

	arguments, _, message = finish(NewToolCallArgumentsStream(), `[1]`)
	if len(arguments) != 0 || message != "Tool arguments must be a JSON object\n\nReceived arguments:\n[1]" {
		t.Fatalf("non-object arguments are reported: %#v, %q", arguments, message)
	}

	// Replace starts over when the text is not a continuation; Finish parses what the stream now holds.
	replaced := NewToolCallArgumentsStream()
	replaced.Append(`{"stale":1}`)
	replaced.Replace(`{"fresh":2}`)
	arguments, _, message = finish(replaced)
	if message != "" || !reflect.DeepEqual(arguments, JsonObject{"fresh": float64(2)}) {
		t.Fatalf("Finish after Replace = %#v, %q", arguments, message)
	}
}
