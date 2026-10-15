package ai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

func validityToolCall(id string) ToolCall {
	return ToolCall{ID: id, Name: "read", Arguments: JsonObject{"path": id}}
}

func validityToolResult(id string) ToolResultMessage {
	return ToolResultMessage{ToolCallID: id, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "result " + id}}}
}

// orphanAfterSummary is the history that sent HTTP 400 to the owner: /tree navigation landed between the results of parallel calls, so a branch summary sat between result a and the synthetic results of b and c that were placed after it.
func orphanAfterSummary() []Message {
	return []Message{
		UserMessage{Content: UserText("start")},
		AssistantMessage{Content: []AssistantContentBlock{validityToolCall("a"), validityToolCall("b"), validityToolCall("c")}, StopReason: StopReasonToolUse},
		validityToolResult("a"),
		UserMessage{Content: UserContentBlocks{TextContent{Text: "branch summary"}}},
		validityToolResult("b"),
		validityToolResult("c"),
		UserMessage{Content: UserText("next prompt")},
	}
}

func validToolFlow() []Message {
	return []Message{
		UserMessage{Content: UserText("start")},
		AssistantMessage{Content: []AssistantContentBlock{validityToolCall("a"), validityToolCall("b")}, StopReason: StopReasonToolUse},
		validityToolResult("a"),
		validityToolResult("b"),
		AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "done"}}, StopReason: StopReasonStop},
		UserMessage{Content: UserText("next prompt")},
	}
}

// textAfterCall is a valid tool flow whose assistant message carries text after its tool call, as a model may stream it. Pi's Responses converter emits the blocks in order (openai-responses-shared.ts convertResponsesMessages: a function_call item, then a message item), and OpenAI documents only a missing output or a missing call as errors.
func textAfterCall() []Message {
	return []Message{
		UserMessage{Content: UserText("start")},
		AssistantMessage{Content: []AssistantContentBlock{validityToolCall("a"), TextContent{Text: "reading it now"}}, StopReason: StopReasonToolUse},
		validityToolResult("a"),
		AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "done"}}, StopReason: StopReasonStop},
		UserMessage{Content: UserText("next prompt")},
	}
}

// A provider that receives a tool result with no tool call in the previous assistant message is a bug in the code that built the history, whatever the provider answers. In a test binary the request checker records it on every API's send path: wire bodies for the JSON APIs, the Converse input for Bedrock, and the transcript for every provider.
func TestProviderSendSeamChecksEveryAPIsRequest(t *testing.T) {
	for _, spec := range []string{
		"anthropic/claude-sonnet-4-5", "openai/gpt-5", "azure/gpt-4.1", "openai-codex/gpt-5.5", "groq/llama-3.1-8b-instant",
		"google/gemini-2.5-flash", "mistral/devstral-medium-latest", "amazon-bedrock/global.anthropic.claude-sonnet-4-5-20250929-v1:0",
	} {
		t.Run(spec, func(t *testing.T) {
			model, ok := LookupModelExact(spec)
			if !ok {
				t.Fatalf("model %s is not in the catalog", spec)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"rejected"}}`))
			}))
			defer server.Close()
			provider := newMatrixProvider(t, model, server.URL, false)
			t.Cleanup(func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			})
			send := func(messages []Message) []string {
				ctx, collector := requestvalidity.Collect(t.Context())
				stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: messages}), StreamOptions{
					Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}, Transport: TransportSSE,
				})
				if err == nil && stream != nil {
					_ = stream.Result()
				}
				return collector.Violations()
			}
			if got := send(validToolFlow()); len(got) != 0 {
				t.Fatalf("a valid tool flow was reported: %v", got)
			}
			if got := send(textAfterCall()); len(got) != 0 {
				t.Fatalf("text after a tool call in one assistant message was reported: %v", got)
			}
			got := send(orphanAfterSummary())
			if len(got) == 0 {
				t.Fatal("the orphan tool results were not reported")
			}
			// The transcript check reports the history for every provider; the API's own check reports the request its converter built.
			wireAPI := string(model.API)
			if wireAPI == string(APIAzureOpenAIResponses) || wireAPI == string(APIOpenAICodexResponses) {
				wireAPI = string(APIOpenAIResponses)
			}
			rule := " result-has-call"
			if model.API == APIBedrockConverseStream {
				// PiG's Converse converter merges adjacent user messages (D116), so the orphan results land in the summary's user message after its text.
				rule = " result-first"
			}
			if !strings.Contains(strings.Join(got, "\n"), wireAPI+rule) {
				t.Fatalf("violations = %v, want a %s%s from the converted request", got, wireAPI, rule)
			}
		})
	}
}

// The faux providers serialize no provider body, so the transcript check is the only one they get.
func TestFauxProviderTranscriptIsChecked(t *testing.T) {
	provider := NewFauxProvider(FauxConfig{})
	provider.SetResponses([]FauxResponseStep{fauxUpstreamText("one"), fauxUpstreamText("two")})
	ctx, collector := requestvalidity.Collect(t.Context())
	if stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: validToolFlow()}), StreamOptions{}); err != nil {
		t.Fatal(err)
	} else {
		_ = stream.Result()
	}
	if got := collector.Violations(); len(got) != 0 {
		t.Fatalf("a valid tool flow was reported: %v", got)
	}
	if stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: orphanAfterSummary()}), StreamOptions{}); err != nil {
		t.Fatal(err)
	} else {
		_ = stream.Result()
	}
	if got := collector.Violations(); len(got) == 0 || !strings.Contains(got[0], "transcript result-has-call") {
		t.Fatalf("violations = %v, want a transcript result-has-call", got)
	}
}
