package ai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func namespaceOutput(t *testing.T, custom bool) *AssistantMessage {
	t.Helper()
	kind, id, name, field, value := "function_call", "fc_test", "lookup", "arguments", `{"value":"hello"}`
	var grammar map[string]string
	if custom {
		kind, id, name, field, value = "custom_tool_call", "ctc_test", "query", "input", "hello"
		grammar = map[string]string{"query": "input"}
	}
	sse := fmt.Sprintf("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":%q,\"id\":%q,\"call_id\":\"call_test\",\"name\":%q,%q:\"\"}}\n\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":%q,\"id\":%q,\"call_id\":\"call_test\",\"name\":%q,%q:%q,\"namespace\":\"dynamic_tools\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\"}}\n\n", kind, id, name, field, kind, id, name, field, value)
	provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{Model: "gpt-5.4", ProviderID: "openai"}}
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "openai", "gpt-5.4")
	provider.parseResponsesSSE(t.Context(), strings.NewReader(sse), builder, grammar)
	return builder.stream.Result()
}

func TestAzureResponsesPreservesAPIIdentityForNativeReplay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()
	provider := NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{APIKey: "test", Model: "gpt-5-mini", ProviderID: "custom-azure", BaseURL: server.URL})
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.API != APIAzureOpenAIResponses || result.Provider != "custom-azure" || result.StopReason != StopReasonStop {
		t.Fatalf("result=%#v", result)
	}
}

func TestOpenAIResponsesNamespaceUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/openai-responses-namespace.test.ts:114
	t.Run("omits an absent error message", func(t *testing.T) {
		data, err := json.Marshal(namespaceOutput(t, false))
		if err != nil {
			t.Fatal(err)
		}
		var message map[string]json.RawMessage
		if err := json.Unmarshal(data, &message); err != nil {
			t.Fatal(err)
		}
		if _, ok := message["errorMessage"]; ok {
			t.Fatalf("errorMessage present: %s", data)
		}
	})
	for _, custom := range []bool{false, true} {
		name := "round-trips a function namespace received only on output_item.done"
		// .upstream/v0.87.1/packages/ai/test/openai-responses-namespace.test.ts:123,150
		if custom {
			name = "round-trips a custom-tool namespace received only on output_item.done"
		}
		t.Run(name, func(t *testing.T) {
			output := namespaceOutput(t, custom)
			if len(output.Content) != 1 {
				t.Fatal(output)
			}
			call, ok := output.Content[0].(ToolCall)
			if !ok {
				t.Fatalf("content=%#v", output.Content)
			}
			id, name, args := "fc_test", "lookup", JsonObject{"value": "hello"}
			var grammar map[string]string
			if custom {
				id, name, args = "ctc_test", "query", JsonObject{"input": "hello"}
				grammar = map[string]string{"query": "input"}
			}
			if call.ID != "call_test|"+id || call.Name != name || call.Namespace != "dynamic_tools" || !reflect.DeepEqual(call.Arguments, args) {
				t.Fatalf("call=%#v", call)
			}
			provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{Model: "gpt-5.4", ProviderID: "openai"}}
			input, err := provider.convertMessages([]Message{*output}, grammar)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			want := `[{"type":"function_call","id":"fc_test","call_id":"call_test","name":"lookup","arguments":"{\"value\":\"hello\"}","namespace":"dynamic_tools"}]`
			if custom {
				want = `[{"type":"custom_tool_call","id":"ctc_test","call_id":"call_test","name":"query","input":"hello","namespace":"dynamic_tools"}]`
			}
			assertShapeJSON(t, data, want)
		})
	}
	// .upstream/v0.87.1/packages/ai/test/openai-responses-namespace.test.ts:178
	t.Run("drops namespaces when the target cannot replay their load items", func(t *testing.T) {
		output := AssistantMessage{API: APIOpenAIResponses, Provider: "openai", Model: "gpt-5.4", Content: []AssistantContentBlock{
			ToolCall{ID: "call_function|fc_test", Name: "lookup", Arguments: JsonObject{"value": "hello"}, Namespace: "dynamic_tools"},
			ToolCall{ID: "call_custom|ctc_test", Name: "query", Arguments: JsonObject{"input": "hello"}, Namespace: "dynamic_tools"},
		}}
		for _, cfg := range []OpenAIResponsesConfig{{Model: "gpt-5.2", ProviderID: "openai"}, {Model: "gpt-5.4", ProviderID: "azure"}, {Model: "gpt-5.3-codex-spark", ProviderID: "openai-codex", Codex: true}} {
			provider := &openAIResponsesProvider{cfg: cfg}
			input, err := provider.convertMessages([]Message{output}, map[string]string{"query": "input"})
			if err != nil {
				t.Fatal(err)
			}
			if len(input) != 2 || input[0].Type != "function_call" || input[1].Type != "custom_tool_call" {
				t.Fatalf("input=%#v", input)
			}
			data, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "namespace") {
				t.Fatalf("namespace leaked: %s", data)
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-responses-namespace.test.ts:226
	t.Run("does not add a namespace to ordinary function calls", func(t *testing.T) {
		output := AssistantMessage{API: APIOpenAIResponses, Provider: "openai", Model: "gpt-5.4", Content: []AssistantContentBlock{ToolCall{ID: "call_test|fc_test", Name: "lookup", Arguments: JsonObject{"value": "hello"}}}}
		provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{Model: "gpt-5.4", ProviderID: "openai"}}
		input, err := provider.convertMessages([]Message{output}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(input) != 1 || input[0].Type != "function_call" {
			t.Fatalf("input=%#v", input)
		}
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "namespace") {
			t.Fatalf("namespace fabricated: %s", data)
		}
	})
}
