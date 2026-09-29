package ai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type toolWithoutResultCase struct {
	name, provider, model string
	api                   API
	oauth                 bool
	effort                string
}

func TestToolCallWithoutResultUpstream(t *testing.T) {
	for _, tc := range toolWithoutResultUpstreamCases() {
		t.Run(tc.provider+"/"+tc.model+"/"+tc.name, func(t *testing.T) {
			generated, ok := LookupModelExact(tc.provider + "/" + tc.model)
			if !ok {
				t.Fatal("missing upstream model")
			}
			model := *generated
			if tc.api != "" {
				model.API = tc.api
				model.Compat = nil
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := decodeMatrixRequest(r)
				if err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				switch calls.Add(1) {
				case 1:
					if !jsonContainsText(body, "Please calculate 25 * 18 using the calculate tool.") {
						t.Error("missing original calculation prompt")
					}
					writeMatrixToolCall(t, w, model.API, "calc00001", "calculate", JsonObject{"expression": "25 * 18"})
				case 2:
					// transform-messages.ts:173-185 repairs the missing result before the next user turn. A faux endpoint must not accept the unrepaired history unconditionally.
					if !jsonContainsText(body, "No result provided") || !jsonContainsText(body, "Never mind, just tell me what is 2+2?") {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						_, _ = io.WriteString(w, `{"error":{"message":"missing tool result"}}`)
						return
					}
					writeMatrixUsageResponse(t, w, model.API, "4", false)
				default:
					t.Error("unexpected extra request")
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			provider := newMatrixProvider(t, &model, server.URL, tc.oauth)
			defer func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			}()
			options := StreamOptions{IsReasoning: model.Reasoning, ReasoningEffort: tc.effort, ModelCost: model.ToModel().CostRates(), Transport: TransportSSE, Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}}
			request := Context{SystemPrompt: "You are a helpful assistant. Use the calculate tool when asked to perform calculations.", Tools: []ToolSchema{{Name: "calculate", Description: "Evaluate mathematical expressions", Parameters: map[string]any{"type": "object", "properties": map[string]any{"expression": map[string]any{"type": "string", "description": "The mathematical expression to evaluate"}}, "required": []string{"expression"}}}}, Messages: []Message{UserMessage{Content: UserText("Please calculate 25 * 18 using the calculate tool."), Timestamp: 1}}}
			complete := func() *AssistantMessage {
				stream, err := provider.Stream(t.Context(), NormalizeContext(request), options)
				if err != nil {
					t.Fatal(err)
				}
				return stream.Result()
			}
			first := complete()
			hasTool := false
			for _, block := range first.Content {
				if _, ok := block.(ToolCall); ok {
					hasTool = true
				}
			}
			if !hasTool {
				t.Fatalf("expected assistant to make a tool call: %+v", first)
			}
			request.Messages = append(request.Messages, *first, UserMessage{Content: UserText("Never mind, just tell me what is 2+2?"), Timestamp: 2})
			second := complete()
			if second.StopReason == StopReasonError {
				t.Fatalf("second response error: %s", second.ErrorMessage)
			}
			if len(second.Content) == 0 {
				t.Fatal("empty second response")
			}
			var texts []string
			toolCalls := 0
			for _, block := range second.Content {
				switch block := block.(type) {
				case TextContent:
					texts = append(texts, block.Text)
				case ToolCall:
					toolCalls++
				}
			}
			if toolCalls == 0 && len(strings.Join(texts, " ")) == 0 {
				t.Fatal("second response has neither tool calls nor text")
			}
			if second.StopReason != StopReasonStop && second.StopReason != StopReasonToolUse {
				t.Fatalf("unexpected stop reason %s", second.StopReason)
			}
		})
	}
}
