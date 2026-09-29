package ai

import (
	"encoding/json"
	"testing"
)

// packages/ai/src/api/bedrock-converse-stream.ts:1120-1154 preserves tools while lowering auto/any/named choices; none omits the entire configuration.
func TestBedrockToolChoiceWire(t *testing.T) {
	type mode string
	tools := []ToolSchema{{Name: "read", Description: "Read a file", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}}}
	for _, tc := range []struct {
		name   string
		choice any
		tools  []ToolSchema
		want   string
		omit   bool
	}{
		{"omitted", nil, tools, "", false},
		{"auto", "auto", tools, `{"auto":{}}`, false},
		{"any", "any", tools, `{"any":{}}`, false},
		{"none", "none", tools, "", true},
		{"typed auto", mode("auto"), tools, `{"auto":{}}`, false},
		{"typed none", mode("none"), tools, "", true},
		{"empty mode", "", tools, "", false},
		{"named tool", map[string]any{"type": "tool", "name": "read"}, tools, `{"tool":{"name":"read"}}`, false},
		{"named string map", map[string]string{"type": "tool", "name": "read"}, tools, `{"tool":{"name":"read"}}`, false},
		{"named JSON object", JsonObject{"type": "tool", "name": "read"}, tools, `{"tool":{"name":"read"}}`, false},
		{"no tools", "any", nil, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateBedrockConfig(t)
			server, requests := rejectingProviderServer(t)
			provider := NewBedrockProvider("us.anthropic.claude-sonnet-4-5-20250929-v1:0", server.URL)
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Summarize this"), Timestamp: 1}}, Tools: tc.tools}), StreamOptions{ToolChoice: tc.choice, CacheRetention: CacheRetentionNone, Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"}})
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			var payload map[string]json.RawMessage
			select {
			case request := <-requests:
				if err := json.Unmarshal(request.body, &payload); err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatalf("request did not reach transport: %+v", result)
			}
			if tc.omit {
				if value, present := payload["toolConfig"]; present {
					t.Fatalf("toolConfig must be omitted, got %s", value)
				}
				return
			}
			var config map[string]json.RawMessage
			if err := json.Unmarshal(payload["toolConfig"], &config); err != nil {
				t.Fatal(err)
			}
			assertShapeJSON(t, config["tools"], `[{"toolSpec":{"name":"read","description":"Read a file","inputSchema":{"json":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}}}]`)
			if tc.want == "" {
				if value, present := config["toolChoice"]; present {
					t.Fatalf("toolChoice must be omitted, got %s", value)
				}
			} else {
				assertShapeJSON(t, config["toolChoice"], tc.want)
			}
		})
	}
}

func BenchmarkBedrockToolChoice(b *testing.B) {
	tools := []ToolSchema{{Name: "read", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}}}
	for _, tc := range []struct {
		name   string
		choice any
	}{
		{"none", "none"}, {"auto", "auto"}, {"named", JsonObject{"type": "tool", "name": "read"}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				config, err := convertBedrockTools(tools, tc.choice, false)
				if err != nil || (config == nil && tc.name != "none") {
					b.Fatalf("config=%+v error=%v", config, err)
				}
			}
		})
	}
}

// The none check precedes strict sampling validation in Pi's convertToolConfig.
func TestBedrockToolChoiceNoneBypassesStrictSampling(t *testing.T) {
	isolateBedrockConfig(t)
	server, requests := rejectingProviderServer(t)
	provider := NewBedrockProvider("amazon.nova-lite-v1:0", server.URL)
	tool := ToolSchema{Name: "read", Parameters: map[string]any{"type": "object"}, ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "require"}}
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}, Tools: []ToolSchema{tool}}), StreamOptions{ToolChoice: "none", Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"}})
	if err != nil {
		t.Fatalf("none must bypass strict tool conversion: %v", err)
	}
	stream.Result()
	select {
	case request := <-requests:
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(request.body, &payload); err != nil {
			t.Fatal(err)
		}
		if config, present := payload["toolConfig"]; present {
			t.Fatalf("toolConfig must be omitted: %s", config)
		}
	default:
		t.Fatal("request did not reach transport")
	}
}
