package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func reasoningReplayTool() ToolSchema {
	return ToolSchema{Name: "double_number", Description: "Doubles a number and returns the result", Parameters: JsonObject{"type": "object", "properties": JsonObject{"value": JsonObject{"type": "number", "description": "A number to double"}}, "required": []string{"value"}}}
}

const replayUser = "Use the double_number tool to double 21."
const replayFollowup = "What was the result? Answer with just the number."

type reasoningReplayTestCase struct {
	name               string
	aborted, anthropic bool
}

func reasoningReplayCases() []reasoningReplayTestCase {
	return []reasoningReplayTestCase{
		// .upstream/v0.87.1/packages/ai/test/openai-responses-reasoning-replay-e2e.test.ts:18
		{"skips reasoning-only history after an aborted turn", true, false},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-reasoning-replay-e2e.test.ts:83
		{"handles same-provider different-model handoff with tool calls", false, false},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-reasoning-replay-e2e.test.ts:184
		{"handles cross-provider handoff from Anthropic to OpenAI Codex", false, true},
	}
}

func TestResponsesReasoningReplayUpstream(t *testing.T) {
	for _, tc := range reasoningReplayCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("hermetic", func(t *testing.T) {
				base := reasoningReplayEndpoint(t, tc.aborted, tc.anthropic)
				runReasoningReplayCase(t, t.Context(), tc.aborted, tc.anthropic, "test-openai", "test-anthropic", base)
			})
		})
	}
}
func runReasoningReplayCase(t *testing.T, ctx context.Context, aborted, anthropic bool, openaiKey, anthropicKey, base string) {
	t.Helper()
	firstModel, firstSystem := "gpt-5-mini", "You are a helpful assistant. Always use the tool when asked."
	if aborted {
		firstSystem = "You are a helpful assistant. Use the tool."
	}
	var firstProvider Provider
	options := StreamOptions{ReasoningEffort: "high", IsReasoning: true}
	if anthropic {
		firstModel = "claude-sonnet-4-5"
		firstProvider = NewAnthropicProvider(AnthropicConfig{APIKey: anthropicKey, Model: firstModel, ProviderID: "anthropic", BaseURL: base})
		// upstream: packages/ai/test/openai-responses-reasoning-replay-e2e.test.ts:215-229 uses raw provider options, not a simple reasoning-level budget.
		options = StreamOptions{ThinkingEnabled: new(true), ThinkingBudgetTokens: new(5000)}
	} else {
		firstProvider = NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: openaiKey, Model: firstModel, ProviderID: "openai", BaseURL: base, IsReasoning: true})
	}
	defer func() { _ = firstProvider.Close() }()
	user := UserMessage{Content: UserText(replayUser), Timestamp: 1}
	firstStream, err := firstProvider.Stream(ctx, NormalizeContext(Context{SystemPrompt: firstSystem, Messages: []Message{user}, Tools: []ToolSchema{reasoningReplayTool()}}), options)
	if err != nil {
		t.Fatal(err)
	}
	first := firstStream.Result()
	var call *ToolCall
	var thinking *ThinkingContent
	for _, block := range first.Content {
		switch block := block.(type) {
		case ToolCall:
			if call == nil {
				call = &block
			}
		case ThinkingContent:
			if block.ThinkingSignature != "" && thinking == nil {
				thinking = &block
			}
		}
	}
	secondModel, secondSystem, followup := "gpt-5.5", "You are a helpful assistant. Answer concisely.", replayFollowup
	messages := []Message{user}
	if aborted {
		if thinking == nil {
			t.Fatalf("Missing thinking signature from OpenAI Responses: %#v", first)
		}
		corrupted := *first
		corrupted.Content = []AssistantContentBlock{*thinking}
		corrupted.StopReason = StopReasonAborted
		messages = append(messages, corrupted)
		secondModel, secondSystem, followup = "gpt-5-mini", "You are a helpful assistant.", "Say hello to confirm you can continue."
	} else {
		if call == nil {
			t.Fatalf("Missing tool call: %#v", first)
		}
		messages = append(messages, *first, ToolResultMessage{ToolCallID: call.ID, ToolName: call.Name, Content: []ToolResultMessageContent{TextContent{Text: "42"}}, Timestamp: 2})
	}
	messages = append(messages, UserMessage{Content: UserText(followup), Timestamp: 3})
	history := Context{SystemPrompt: secondSystem, Messages: messages, Tools: []ToolSchema{reasoningReplayTool()}}
	secondProvider := NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: openaiKey, Model: secondModel, ProviderID: "openai", BaseURL: base, IsReasoning: true})
	defer func() { _ = secondProvider.Close() }()
	var captured any
	stream, err := secondProvider.Stream(ctx, NormalizeContext(history), StreamOptions{ReasoningEffort: "high", IsReasoning: true, OnPayload: func(value any, _ *Model) (any, error) { captured = value; return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	if result.StopReason == StopReasonError || result.ErrorMessage != "" || len(result.Content) == 0 {
		t.Fatalf("response=%#v payload=%#v", result, captured)
	}
	if !aborted {
		var text strings.Builder
		for _, block := range result.Content {
			if block, ok := block.(TextContent); ok {
				text.WriteString(block.Text)
			}
		}
		if !strings.Contains(text.String(), "42") {
			t.Fatalf("response text=%q", text.String())
		}
	}
}
func reasoningReplayEndpoint(t *testing.T, aborted, anthropic bool) string {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var payload map[string]json.RawMessage
		if err = json.Unmarshal(body, &payload); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		count := calls.Add(1)
		wantModel := "gpt-5-mini"
		if count == 1 && anthropic {
			wantModel = "claude-sonnet-4-5"
		} else if count == 2 && !aborted {
			wantModel = "gpt-5.5"
		}
		var requestedModel string
		if err := json.Unmarshal(payload["model"], &requestedModel); err != nil || requestedModel != wantModel {
			t.Errorf("replay request %d model=%q, want %q (decode: %v)", count, requestedModel, wantModel, err)
			http.Error(w, "changed upstream replay model", http.StatusBadRequest)
			return
		}
		if !(count == 1 && anthropic) {
			var reasoning map[string]string
			if err := json.Unmarshal(payload["reasoning"], &reasoning); err != nil || !reflect.DeepEqual(reasoning, map[string]string{"effort": "high", "summary": "auto"}) {
				t.Errorf("replay request %d reasoning=%#v (decode: %v)", count, reasoning, err)
				http.Error(w, "changed upstream reasoning effort", http.StatusBadRequest)
				return
			}
		}
		if count == 1 {
			if !strings.Contains(string(body), "double_number") || !strings.Contains(string(body), replayUser) {
				http.Error(w, "missing original tool request", 400)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if anthropic {
				// upstream: packages/ai/src/api/anthropic-messages.ts:1177-1182 preserves the requested 5000-token raw budget and defaults display to summarized.
				var thinking any
				if err := json.Unmarshal(payload["thinking"], &thinking); err != nil {
					t.Error(err)
					http.Error(w, "invalid thinking payload", http.StatusBadRequest)
					return
				}
				want := map[string]any{"type": "enabled", "budget_tokens": float64(5000), "display": "summarized"}
				if !reflect.DeepEqual(thinking, want) {
					t.Errorf("thinking=%#v, want %#v", thinking, want)
					http.Error(w, "wrong raw thinking budget", http.StatusBadRequest)
					return
				}
				_, _ = fmt.Fprint(w, reasoningReplayAnthropicSSE())
			} else {
				_, _ = fmt.Fprint(w, reasoningReplayFirstSSE())
			}
			return
		}
		if count != 2 {
			http.Error(w, "unexpected extra request", 400)
			return
		}
		var input []map[string]json.RawMessage
		if err = json.Unmarshal(payload["input"], &input); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		reasoning, callsSeen, results, assistantText := 0, 0, 0, false
		for _, item := range input {
			switch string(item["type"]) {
			case `"reasoning"`:
				reasoning++
			case `"function_call"`:
				callsSeen++
				if !anthropic && item["id"] != nil {
					http.Error(w, "same-provider handoff retained paired item id", 400)
					return
				}
				var arguments string
				var object map[string]any
				if err := json.Unmarshal(item["arguments"], &arguments); err != nil {
					http.Error(w, "invalid tool arguments string", http.StatusBadRequest)
					return
				}
				if err := json.Unmarshal([]byte(arguments), &object); err != nil || !reflect.DeepEqual(object, map[string]any{"value": float64(21)}) {
					http.Error(w, "lost tool arguments", http.StatusBadRequest)
					return
				}
			case `"function_call_output"`:
				results++
				if string(item["output"]) != `"42"` {
					http.Error(w, "lost tool result", 400)
					return
				}
			}
			if string(item["role"]) == `"assistant"` && strings.Contains(string(item["content"]), "Double 21") {
				assistantText = true
			}
		}
		if reasoning != 0 {
			http.Error(w, "orphaned reasoning item", 400)
			return
		}
		if aborted {
			if callsSeen != 0 || results != 0 || assistantText {
				http.Error(w, "aborted assistant leaked into replay", 400)
				return
			}
		} else if callsSeen != 1 || results != 1 || !assistantText {
			http.Error(w, "handoff lost reasoning text or tool history", 400)
			return
		}
		text := "42"
		if aborted {
			text = "Hello"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range codexWSHello("resp_replay", text) {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", frame)
		}
	}))
	t.Cleanup(func() {
		server.Close()
		if calls.Load() != 2 {
			t.Errorf("requests=%d want 2", calls.Load())
		}
	})
	return server.URL
}
func reasoningReplayFirstSSE() string {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"Double 21"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","encrypted_content":"encrypted-reasoning","summary":[{"type":"summary_text","text":"Double 21"}]}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_double","name":"double_number","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"value\":21}"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_double","name":"double_number","arguments":"{\"value\":21}"}}`,
		`{"type":"response.completed","response":{"id":"resp_first","status":"completed"}}`,
	}
	var out strings.Builder
	for _, event := range events {
		fmt.Fprintf(&out, "data: %s\n\n", event)
	}
	return out.String()
}
func reasoningReplayAnthropicSSE() string {
	events := []string{
		`{"type":"message_start","message":{"id":"msg_first","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-5","usage":{"input_tokens":5,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Double 21"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"anthropic-signature"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_double","name":"double_number","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"value\":21}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}`,
		`{"type":"message_stop"}`,
	}
	var out strings.Builder
	for _, event := range events {
		var frame struct{ Type string }
		_ = json.Unmarshal([]byte(event), &frame)
		fmt.Fprintf(&out, "event: %s\ndata: %s\n\n", frame.Type, event)
	}
	return out.String()
}
