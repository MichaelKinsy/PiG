package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// upstream: packages/ai/test/mistral-tool-schema.test.ts
// The upstream case guards the TypeBox symbol keys that Type.Object attaches to a schema; the Mistral SDK validated them and rejected the tool. Go schemas are plain maps with no symbol keys, so the guard that remains is what the case observes: the payload carries the nested schema as plain JSON with strict mode set, and nothing in the request fails schema validation before the connection is attempted.

func TestMistralToolSchemaSerialization(t *testing.T) {
	// upstream: mistral-tool-schema.test.ts:18 "strips TypeBox symbol keys before the SDK validates tool schemas"
	m := mustGeneratedModel(t, "mistral", "devstral-medium-latest")
	provider := NewMistralProvider(MistralConfig{Model: m.ID, BaseURL: "http://127.0.0.1:9", APIKey: "fake-key", ExtraHeaders: m.Headers, Reasoning: m.Reasoning})
	request := NormalizeContext(Context{
		Messages: []Message{UserMessage{Content: UserText("Hi"), Timestamp: 1}},
		Tools: []ToolSchema{{
			Name:        "inspect_schema",
			Description: "Inspect the schema",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"nested": map[string]any{
						"type":       "object",
						"properties": map[string]any{"value": map[string]any{"type": "string"}},
						"required":   []any{"value"},
					},
				},
				"required": []any{"nested"},
			},
			ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "require"},
		}},
	})
	var captured map[string]any
	stream, err := provider.Stream(t.Context(), request, StreamOptions{OnPayload: func(payload any, _ *Model) (any, error) {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &captured); err != nil {
			t.Fatal(err)
		}
		return payload, nil
	}})
	// Provider.Stream returns a transport failure as its error where upstream's complete resolves an error message.
	failure := ""
	if err != nil {
		failure = err.Error()
	} else if response := stream.Result(); response.StopReason == StopReasonError {
		failure = response.ErrorMessage
	}

	tools, _ := captured["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("payload tools = %#v", captured["tools"])
	}
	function, _ := tools[0].(map[string]any)["function"].(map[string]any)
	if function["strict"] != true {
		t.Errorf("function.strict = %#v, want true", function["strict"])
	}
	parameters, _ := function["parameters"].(map[string]any)
	properties, _ := parameters["properties"].(map[string]any)
	nested, _ := properties["nested"].(map[string]any)
	nestedProperties, _ := nested["properties"].(map[string]any)
	if nestedProperties["value"] == nil || nested["type"] != "object" {
		t.Errorf("nested schema = %#v", parameters)
	}
	if failure == "" {
		t.Error("the request succeeded, but nothing listens on 127.0.0.1:9")
	}
	if strings.Contains(failure, "Input validation failed") {
		t.Errorf("failure = %q, the request failed schema validation", failure)
	}
}
