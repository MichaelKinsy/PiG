package ai

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRequiresToolCallId(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"claude-sonnet-4", true}, {"gpt-oss-120b", true}, {"gemini-3-pro", true},
		{"gemini-3-flash", true}, {"gemini-live-3-pro", true}, {"GEMINI-3-PRO", true},
		{"gemini-2.5-flash", false}, {"gemini-2.0-flash", false}, {"gemini-1.5-pro", false},
		{"gpt-4o", false}, {"o3-mini", false},
	}
	for _, test := range tests {
		if got := requiresToolCallId(test.model); got != test.want {
			t.Errorf("requiresToolCallId(%q) = %v, want %v", test.model, got, test.want)
		}
	}
}

func googleToolCallMessages(id string) []Message {
	return []Message{
		AssistantMessage{Provider: "google", Model: "gemini-3-pro", Content: []AssistantContentBlock{
			ToolCall{ID: id, Name: "bash", Arguments: JsonObject{"command": "ls"}},
		}},
		ToolResultMessage{ToolCallID: id, ToolName: "bash", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}},
	}
}

func TestGoogleConvertMessages_EmitsToolCallIdWhenRequired(t *testing.T) {
	output := geminiConvertMessages(googleToolCallMessages("call_abc|item_def"), "google", "gemini-3-pro", true)
	const wantID = "call_abc_item_def"
	if call := findFunctionCall(output); call == nil || call.ID != wantID {
		t.Fatalf("function call = %#v", call)
	}
	if response := findFunctionResponse(output); response == nil || response.ID != wantID {
		t.Fatalf("function response = %#v", response)
	}
}

func TestGoogleConvertMessages_OmitsToolCallIdWhenNotRequired(t *testing.T) {
	messages := googleToolCallMessages("call_abc")
	messages[0] = AssistantMessage{Provider: "google", Model: "gemini-2.5-flash", Content: []AssistantContentBlock{
		ToolCall{ID: "call_abc", Name: "bash", Arguments: JsonObject{"command": "ls"}},
	}}
	output := geminiConvertMessages(messages, "google", "gemini-2.5-flash", true)
	if call := findFunctionCall(output); call == nil || call.ID != "" {
		t.Fatalf("function call = %#v", call)
	}
	if response := findFunctionResponse(output); response == nil || response.ID != "" {
		t.Fatalf("function response = %#v", response)
	}
}

func TestGoogleConvertMessages_TruncatesToolCallIdTo64(t *testing.T) {
	id := strings.Repeat("a", 100)
	messages := []Message{AssistantMessage{Provider: "google", Model: "gemini-3-pro", Content: []AssistantContentBlock{
		ToolCall{ID: id, Name: "bash", Arguments: JsonObject{}},
	}}}
	call := findFunctionCall(geminiConvertMessages(messages, "google", "gemini-3-pro", true))
	if call == nil || len(call.ID) != 64 {
		t.Fatalf("function call = %#v", call)
	}
}

func findFunctionCall(contents []geminiContent) *geminiFunctionCall {
	for _, content := range contents {
		for index := range content.Parts {
			if content.Parts[index].FunctionCall != nil {
				return content.Parts[index].FunctionCall
			}
		}
	}
	return nil
}

func findFunctionResponse(contents []geminiContent) *geminiFuncResponse {
	for _, content := range contents {
		for index := range content.Parts {
			if content.Parts[index].FunctionResponse != nil {
				return content.Parts[index].FunctionResponse
			}
		}
	}
	return nil
}

func googleStreamedToolCallIDs(t *testing.T, sse string) []string {
	t.Helper()
	var ids []string
	for _, block := range googleTerminalMessage(t, runGoogleSSE(t, sse)).Content {
		if call, ok := block.(ToolCall); ok {
			ids = append(ids, call.ID)
		}
	}
	return ids
}

// Pi generates `${name}_${Date.now()}_${++toolCallCounter}` when the ID is missing or duplicates an earlier call in the same message (google-generative-ai.ts:195-200).
func TestGoogleStreamGeneratedToolCallIDMatchesPi(t *testing.T) {
	sse := `data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"read","args":{"path":"a"}}},{"functionCall":{"id":"call_x","name":"read","args":{"path":"b"}}},{"functionCall":{"id":"call_x","name":"bash","args":{"command":"ls"}}}]},"finishReason":"STOP"}]}

`
	before := time.Now().UnixMilli()
	counter := googleGenerativeToolCallCounter.Load()
	ids := googleStreamedToolCallIDs(t, sse)
	after := time.Now().UnixMilli()
	if len(ids) != 3 || ids[1] != "call_x" {
		t.Fatalf("ids = %q", ids)
	}
	for index, want := range []struct {
		name    string
		counter int64
	}{{"read", counter + 1}, {"bash", counter + 2}} {
		id := ids[[]int{0, 2}[index]]
		parts := strings.Split(id, "_")
		if len(parts) != 3 || parts[0] != want.name || parts[2] != strconv.FormatInt(want.counter, 10) {
			t.Fatalf("generated id %q, want %s_<ms>_%d", id, want.name, want.counter)
		}
		millis, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || millis < before || millis > after {
			t.Fatalf("generated id %q timestamp outside [%d, %d]", id, before, after)
		}
	}
}

// google-vertex.ts:66 declares its own module counter; Vertex IDs do not advance the Generative AI counter.
func TestGoogleToolCallIDCountersArePerAPI(t *testing.T) {
	generative := googleGenerativeToolCallCounter.Load()
	vertex := googleVertexToolCallCounter.Load()
	id := googleToolCallID(APIGoogleVertex, "read", "", nil)
	if !strings.HasSuffix(id, "_"+strconv.FormatInt(vertex+1, 10)) || googleGenerativeToolCallCounter.Load() != generative {
		t.Fatalf("vertex id %q; generative counter %d -> %d", id, generative, googleGenerativeToolCallCounter.Load())
	}
	if got := googleToolCallID(APIGoogleGenerativeAI, "read", "call_1", nil); got != "call_1" {
		t.Fatalf("provided unique id replaced: %q", got)
	}
}

// google-generative-ai.ts:198-200 interpolates part.functionCall.name unguarded: a missing name becomes "undefined" and a null name "null", while an empty string stays empty. The tool call's own name is `name || ""`.
func TestGoogleStreamGeneratedToolCallIDStringifiesMissingNameLikeJavaScript(t *testing.T) {
	sse := `data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"args":{}}},{"functionCall":{"name":null,"args":{}}},{"functionCall":{"name":"","args":{}}}]},"finishReason":"STOP"}]}

`
	ids := googleStreamedToolCallIDs(t, sse)
	if len(ids) != 3 {
		t.Fatalf("ids = %q", ids)
	}
	for index, prefix := range []string{"undefined_", "null_", "_"} {
		if !strings.HasPrefix(ids[index], prefix) || strings.Count(ids[index], "_") != 2 {
			t.Errorf("ids[%d] = %q, want %s<ms>_<n>", index, ids[index], prefix)
		}
	}
}
