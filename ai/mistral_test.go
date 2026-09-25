package ai

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestDeriveMistralToolCallID(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		attempt int
		wantLen int
	}{
		{"exact length alnum", "abcdefghi", 0, 9},
		{"short id hashed", "abc", 0, 9},
		{"with special chars", "tool-call-123", 0, 9},
		{"retry attempt", "abc", 1, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveMistralToolCallID(tc.id, tc.attempt)
			if len(got) != tc.wantLen {
				t.Errorf("deriveMistralToolCallID(%q, %d) = %q (len %d), want len %d", tc.id, tc.attempt, got, len(got), tc.wantLen)
			}
		})
	}

	// Oracles from upstream deriveMistralToolCallId (shortHash) under Node.
	for _, tc := range []struct {
		id      string
		attempt int
		want    string
	}{
		{"call_abc123def456", 0, "1outjl17g"},
		{"call_abc123def456", 1, "1jvgtx41n"},
		{"toolu_01ABCdef", 0, "9o5szclp9"},
		{"abcdefghi", 1, "mlj703uel"},
		{"---", 0, "1g7e9bufr"},
		{"fc_1|item_2", 2, "509x70141"},
	} {
		if got := deriveMistralToolCallID(tc.id, tc.attempt); got != tc.want {
			t.Errorf("deriveMistralToolCallID(%q, %d) = %q, want upstream %q", tc.id, tc.attempt, got, tc.want)
		}
	}

	// Exact 9-char alnum should pass through
	got := deriveMistralToolCallID("abcdefghi", 0)
	if got != "abcdefghi" {
		t.Errorf("exact passthrough: got %q, want %q", got, "abcdefghi")
	}
}

func TestMistralIDNormalizer(t *testing.T) {
	n := newMistralIDNormalizer()
	id1 := n.normalize("tool-call-1")
	id2 := n.normalize("tool-call-2")
	if id1 == id2 {
		t.Error("different inputs should produce different normalized IDs")
	}
	// Same input should return same output
	if got := n.normalize("tool-call-1"); got != id1 {
		t.Errorf("same input returned different result: %q vs %q", got, id1)
	}
}

func TestMapMistralStopReason(t *testing.T) {
	cases := []struct {
		input       string
		wantReason  StopReason
		wantMessage string
	}{
		{input: "stop", wantReason: StopReasonStop},
		{input: "length", wantReason: StopReasonLength},
		{input: "model_length", wantReason: StopReasonLength},
		{input: "tool_calls", wantReason: StopReasonToolUse},
		{input: "error", wantReason: StopReasonError, wantMessage: "Provider stopped with: error"},
		{input: "unknown", wantReason: StopReasonError, wantMessage: "Provider stopped with: unknown"},
	}
	for _, test := range cases {
		reason, message := mapMistralStopReason(test.input)
		if reason != test.wantReason || message != test.wantMessage {
			t.Errorf("mapMistralStopReason(%q) = %q, %q; want %q, %q", test.input, reason, message, test.wantReason, test.wantMessage)
		}
	}
}

func TestMistralErrorRetainsUsage(t *testing.T) {
	body := io.NopCloser(strings.NewReader(`data: {"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9},"choices":[{"delta":{},"finish_reason":"error"}]}

`))
	builder := newAssistantStreamBuilder(context.Background(), APIMistralConversations, "mistral", "model")
	provider := &mistralProvider{}
	go provider.consumeStream(context.Background(), body, builder)
	result := builder.stream.Result()
	if result.StopReason != StopReasonError || result.ErrorMessage != "Provider stopped with: error" {
		t.Fatalf("result = %#v", result)
	}
	if result.Usage.Input != 7 || result.Usage.Output != 2 || result.Usage.TotalTokens != 9 {
		t.Fatalf("usage = %#v, want input 7 output 2 total 9", result.Usage)
	}
}

func TestMistralStrictToolSchema(t *testing.T) {
	provider := &mistralProvider{}
	tools, err := provider.convertTools([]ToolSchema{{
		Name: "sample", Description: "sample",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"required_value": map[string]any{"type": "string"},
			"optional_value": map[string]any{"type": "number"},
		}, "required": []any{"required_value"}},
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"},
	}})
	if err != nil {
		t.Fatalf("convert tools: %v", err)
	}
	body, err := json.Marshal(tools[0])
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Function struct {
			Strict     bool           `json:"strict"`
			Parameters map[string]any `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if !wire.Function.Strict || wire.Function.Parameters["additionalProperties"] != false {
		t.Fatalf("strict Mistral tool = %s", body)
	}
	optional := wire.Function.Parameters["properties"].(map[string]any)["optional_value"].(map[string]any)
	if _, ok := optional["anyOf"]; !ok {
		t.Fatalf("optional schema = %#v, want nullable anyOf", optional)
	}
}

func TestUsesMistralReasoningEffort(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"mistral-small-2603", true},
		{"mistral-small-latest", true},
		{"mistral-medium-3.5", true},
		{"devstral-medium-latest", false},
	}
	for _, tc := range cases {
		if got := usesMistralReasoningEffort(tc.model); got != tc.want {
			t.Errorf("usesMistralReasoningEffort(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestUsesMistralPromptModeReasoning(t *testing.T) {
	if usesMistralPromptModeReasoning("mistral-medium-3.5", true) {
		t.Fatal("mistral-medium-3.5 should not use prompt_mode reasoning")
	}
	if !usesMistralPromptModeReasoning("devstral-medium-latest", true) {
		t.Fatal("devstral-medium-latest reasoning model should use prompt_mode reasoning")
	}
	if usesMistralPromptModeReasoning("devstral-medium-latest", false) {
		t.Fatal("non-reasoning model should not use prompt_mode reasoning")
	}
}

func TestNewMistralProvider(t *testing.T) {
	p := NewMistralProvider(MistralConfig{
		APIKey: "test-key",
		Model:  "mistral-small-latest",
	})
	if p.ID() != "mistral" {
		t.Errorf("ID() = %q, want %q", p.ID(), "mistral")
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
}

func TestMistralReasoningEffortUsesThinkingLevelMap(t *testing.T) {
	model := &Model{
		Capabilities: ModelCapabilities{MaxThinking: ThinkingXHigh},
		ThinkingLevelMap: ThinkingLevelMap{
			ThinkingXHigh: new("max"),
		},
	}
	if got := ClampThinkingLevel(model, ThinkingXHigh); got != ThinkingXHigh {
		t.Fatalf("ClampThinkingLevel(xhigh) = %q", got)
	}
	if got := *model.ThinkingLevelMap[ThinkingXHigh]; got != "max" {
		t.Fatalf("mapped effort = %q, want max", got)
	}
}
