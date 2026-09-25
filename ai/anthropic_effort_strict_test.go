package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestAnthropicManagedEffortAndStrictTools(t *testing.T) {
	var body map[string]any
	var betaHeader string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var err error
		bodyBytes, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Errorf("read request: %v", readErr)
			return
		}
		err = json.Unmarshal(bodyBytes, &body)
		if err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		betaHeader = request.Header.Get("anthropic-beta")
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "event: message_start\n"+
			"data: {\"message\":{\"id\":\"message-1\",\"model\":\"claude-fable-5-1\",\"usage\":{}}}\n\n"+
			"event: message_delta\n"+
			"data: {\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{}}\n\n"+
			"event: message_stop\n"+
			"data: {}\n\n")
	}))
	defer server.Close()

	providerID := "test-anthropic"
	modelID := "managed-test-model"
	provider := NewAnthropicProvider(AnthropicConfig{
		APIKey:     "test-key",
		Model:      modelID,
		ProviderID: providerID,
		BaseURL:    server.URL,
		Compat: &AnthropicMessagesCompat{
			ForceAdaptiveThinking:  ptrBool(true),
			SupportsMidConvoEffort: ptrBool(true),
			SupportsStrictTools:    ptrBool(true),
		},
	})
	tool := ToolSchema{
		Name: "lookup", Description: "Look up a value",
		Parameters: map[string]any{
			"type": "object", "title": "LookupInput", "additionalProperties": false,
			"properties": map[string]any{"value": map[string]any{"type": "string"}},
			"required":   []any{"value"},
		},
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"},
	}
	previous := AssistantMessage{
		Content: []AssistantContentBlock{
			ThinkingContent{Thinking: "reasoning", ThinkingSignature: "signature"},
			TextContent{Text: "answer"},
		},
		API: APIAnthropicMessages, Provider: providerID, Model: modelID,
		ProviderThinkingLevel: "low", StopReason: StopReasonStop, Timestamp: 2,
	}
	transcript := NormalizeContext(Context{
		Tools: []ToolSchema{tool},
		Messages: []Message{
			UserMessage{Content: UserText("one"), Timestamp: 1},
			previous,
			UserMessage{Content: UserText("two"), Timestamp: 3},
		},
	})
	stream, err := provider.Stream(context.Background(), transcript, StreamOptions{Thinking: ThinkingMedium})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	result := stream.Result()
	if result.StopReason != StopReasonStop {
		t.Fatalf("result = reason %q error %q", result.StopReason, result.ErrorMessage)
	}
	if result.ProviderThinkingLevel != "medium" {
		t.Errorf("providerThinkingLevel = %q, want medium", result.ProviderThinkingLevel)
	}

	messages, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("messages = %#v", body["messages"])
	}
	var effortMarkers []string
	var sequence []string
	for _, item := range messages {
		message, _ := item.(map[string]any)
		role, _ := message["role"].(string)
		sequence = append(sequence, role)
		if role != "system" {
			continue
		}
		outputConfig, _ := message["output_config"].(map[string]any)
		if effort, ok := outputConfig["effort"].(string); ok {
			effortMarkers = append(effortMarkers, effort)
		}
	}
	if !slices.Equal(sequence, []string{"user", "system", "assistant", "user", "system"}) {
		t.Errorf("message roles = %v, want historical marker before assistant and active marker last", sequence)
	}
	if !slices.Equal(effortMarkers, []string{"low", "medium"}) {
		t.Errorf("effort markers = %v, want [low medium]", effortMarkers)
	}
	thinking, _ := body["thinking"].(map[string]any)
	binding, _ := thinking["block_binding"].(map[string]any)
	if thinking["type"] != "adaptive" || binding["prefix_mismatch_behavior"] != "drop_block" {
		t.Errorf("thinking = %#v, want adaptive drop_block", thinking)
	}
	outputConfig, _ := body["output_config"].(map[string]any)
	if outputConfig["effort"] != "high" {
		t.Errorf("output_config = %#v, want top-level high", outputConfig)
	}
	if !headerListContains(betaHeader, "mid-conversation-output-config-2026-07-01") || !headerListContains(betaHeader, "thinking-binding-controls-2026-08-01") {
		t.Errorf("anthropic-beta = %q, want managed-effort betas", betaHeader)
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", body["tools"])
	}
	wireTool, _ := tools[0].(map[string]any)
	if wireTool["strict"] != true {
		t.Errorf("tool.strict = %#v, want true", wireTool["strict"])
	}
	inputSchema, _ := wireTool["input_schema"].(map[string]any)
	if inputSchema["title"] != "LookupInput" || inputSchema["additionalProperties"] != false {
		t.Errorf("strict input_schema = %#v, want full schema", inputSchema)
	}
}

func TestAnthropicManagedEffortMapping(t *testing.T) {
	model := &Model{}
	for _, test := range []struct {
		level ThinkingLevel
		want  string
	}{
		{level: "", want: "high"},
		{level: ThinkingOff, want: "high"},
		{level: ThinkingMinimal, want: "low"},
		{level: ThinkingLow, want: "low"},
		{level: ThinkingMedium, want: "medium"},
		{level: ThinkingHigh, want: "high"},
		{level: ThinkingXHigh, want: "xhigh"},
		{level: ThinkingMax, want: "max"},
	} {
		if got := anthropicActiveEffort(model, test.level); got != test.want {
			t.Errorf("anthropicActiveEffort(%q) = %q, want %q", test.level, got, test.want)
		}
	}
}

func TestAnthropicStrictRequiredFailsWhenUnsupported(t *testing.T) {
	provider := NewAnthropicProvider(AnthropicConfig{
		APIKey: "test-key", Model: "strict-test-model", ProviderID: "test-anthropic",
		BaseURL: "http://127.0.0.1:1",
	})
	transcript := NormalizeContext(Context{
		Tools: []ToolSchema{{
			Name: "lookup", Parameters: map[string]any{
				"type": "object", "properties": map[string]any{},
			},
			ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "require"},
		}},
		Messages: []Message{UserMessage{Content: UserText("use it"), Timestamp: 1}},
	})
	_, err := provider.Stream(context.Background(), transcript, StreamOptions{})
	if err == nil || !strings.Contains(err.Error(), `Tool "lookup" requires JSON-schema constrained sampling, but strict tools are unsupported.`) {
		t.Fatalf("Stream error = %v", err)
	}
}

func headerListContains(header, value string) bool {
	for item := range strings.SplitSeq(header, ",") {
		if strings.TrimSpace(item) == value {
			return true
		}
	}
	return false
}
