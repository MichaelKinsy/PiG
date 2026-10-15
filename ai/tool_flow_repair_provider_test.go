package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// pig divergence (D48): every real provider repairs the tool flow of its request when StreamOptions.RepairToolFlow is set, before its
// transformMessages builds the payload, and sends Pi's request otherwise. An orphaned result reaches the native payload without the option
// (transform-messages.ts:36-190 keeps it; Gemini sends it without the ID, so the test finds its text) and is gone with it.
func TestRepairToolFlowReachesEveryProviderPayload(t *testing.T) {
	transcript := NormalizeContext(Context{Messages: []Message{
		UserMessage{Content: UserText("Hi"), Timestamp: 1},
		AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "ok"}}, StopReason: StopReasonStop, Timestamp: 2},
		ToolResultMessage{ToolCallID: "call_orphan_d48", ToolName: "t", Content: []ToolResultMessageContent{TextContent{Text: "stray result d48"}}, Timestamp: 3},
		UserMessage{Content: UserText("go"), Timestamp: 4},
	}})
	for _, api := range []API{APIAnthropicMessages, APIAzureOpenAIResponses, APIBedrockConverseStream, APIGoogleGenerativeAI, APIGoogleVertex, APIMistralConversations, APIOpenAICodexResponses, APIOpenAICompletions, APIOpenAIResponses} {
		for _, repair := range []bool{false, true} {
			name := string(api) + map[bool]string{false: "/as Pi", true: "/repaired"}[repair]
			t.Run(name, func(t *testing.T) {
				model := &Model{ID: "test-model", ProviderMeta: ProviderMetadata{API: api, ProviderID: "test-provider", BaseURL: "https://example.invalid"}, Capabilities: ModelCapabilities{ContextWindow: 10000, MaxOutputTokens: 1000}}
				sentinel := errors.New("captured")
				var payload []byte
				options := StreamOptions{APIKey: "request-key", RepairToolFlow: repair, Env: ProviderEnv{"AWS_REGION": "us-east-1", "AWS_ACCESS_KEY_ID": "k", "AWS_SECRET_ACCESS_KEY": "s"}, OnPayload: func(value any, _ *Model) (any, error) {
					encoded, err := json.Marshal(value)
					if err != nil {
						t.Error(err)
					}
					payload = encoded
					return nil, sentinel
				}}
				ctx := context.Context(t.Context())
				if !repair {
					// Pi's request keeps the orphaned result, which the request validity check reports.
					ctx = requestvalidity.Allow(ctx)
				}
				stream, err := StreamSimple(ctx, model, transcript, options)
				if stream != nil {
					stream.Result()
				}
				if payload == nil {
					t.Fatalf("no payload built (error %v)", err)
				}
				if got := strings.Contains(string(payload), "stray result d48"); got == repair {
					t.Fatalf("orphaned result in the payload: %v, want %v: %s", got, !repair, payload)
				}
			})
		}
	}
}
