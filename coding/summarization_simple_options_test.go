package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// completeSummarization calls pi-ai's completeSimple when the session has no stream override (compaction.ts:634-637), so the request is a simple stream: buildBaseOptions keeps the output budget within the room the model's context window leaves after the request and a 4096-token margin, never below one token (packages/ai/src/api/simple-options.ts:18-22, 46), and an omitted reasoning level sends the API's native thinking off (anthropic-messages.ts and google-generative-ai.ts streamSimple). Real Pi sent max_completion_tokens 1 for a 3000-token window where PiG sent 500.
func TestSummarizationOfARegistryProviderIsASimpleStream(t *testing.T) {
	for _, test := range []struct {
		name          string
		api           ai.API
		window, asked int
		wantMax       int // -1: the 600 the window leaves less the few tokens the request estimates
		wantNative    bool
	}{
		{"window below the safety margin", ai.APIOpenAICompletions, 3000, 800, 1, false},
		{"room left below the request", ai.APIOpenAICompletions, 4096 + 600, 800, -1, false},
		{"plenty of room keeps the request", ai.APIOpenAICompletions, 200000, 800, 800, false},
		{"anthropic thinking off", ai.APIAnthropicMessages, 200000, 800, 800, true},
		{"google thinking off", ai.APIGoogleGenerativeAI, 200000, 800, 800, true},
		{"vertex thinking off", ai.APIGoogleVertex, 200000, 800, 800, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			faux := ai.NewFauxProvider(ai.FauxConfig{})
			var got ai.StreamOptions
			faux.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
				got = options
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("summary")}}.AssistantMessage(), nil
			})})
			model := faux.GetModel()
			model.Capabilities.ContextWindow = test.window
			model.ProviderMeta.API = test.api
			model.Provider = newProviderAttributionProvider(faux, "fixture", "http://127.0.0.1:1", nil, nil)
			messages := []agent.AgentMessage{{User: &agent.UserMessage{Role: "user", Content: ai.UserText("hello")}}}
			if _, _, err := (compaction.ProviderCompleter{}).CompleteSimple(context.Background(), model, "system", messages, ai.StreamOptions{MaxTokens: test.asked}); err != nil {
				t.Fatal(err)
			}
			if test.wantMax < 0 {
				if got.MaxTokens < 500 || got.MaxTokens > 600 {
					t.Fatalf("max tokens sent = %d, want the 600 the window leaves less the request estimate", got.MaxTokens)
				}
			} else if got.MaxTokens != test.wantMax {
				t.Fatalf("max tokens sent = %d, want %d", got.MaxTokens, test.wantMax)
			}
			native := (got.ThinkingEnabled != nil && !*got.ThinkingEnabled) || (got.GoogleThinking != nil && !got.GoogleThinking.Enabled)
			if native != test.wantNative {
				t.Fatalf("native thinking off = %t, want %t (%+v)", native, test.wantNative, got)
			}
		})
	}
}
