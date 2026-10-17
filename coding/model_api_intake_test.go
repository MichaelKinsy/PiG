package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Independent API requests own their already-selected model data; neither registry fallback nor legacy reconstruction may discard it.
func TestIntakeIndependentAPIRequestRetainsThinkingMapAndImageInput(t *testing.T) {
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		t.Run(string(api), func(t *testing.T) {
			requests := make(chan map[string]json.RawMessage, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				requests <- payload
				w.Header().Set("Content-Type", "text/event-stream")
				if api == ai.APIOpenAICompletions {
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
				} else {
					_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
				}
			}))
			defer server.Close()
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			model := &ai.Model{ID: "gpt-5.5", ProviderMeta: ai.ProviderMetadata{API: api, ProviderID: "child-only", BaseURL: server.URL, Reasoning: true}, Input: []string{"text", "image"}, Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingLevelHigh, SupportsImages: true}, ThinkingLevelMap: ai.ThinkingLevelMap{ai.ThinkingHigh: new("low")}}
			ctx := extension.WithModelStreamRequest(t.Context(), extension.ModelStreamRequest{API: true})
			result := services.ModelRuntime().Stream(ctx, model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "describe"}, ai.ImageContent{MimeType: "image/png", Data: "aGk="}}}}}, ai.StreamOptions{APIKey: "child-key", IsReasoning: true, Thinking: ai.ThinkingLevelHigh}).Result()
			if result.StopReason != ai.StopReasonStop {
				t.Fatal(result)
			}
			request := <-requests
			if api == ai.APIOpenAICompletions {
				if string(request["reasoning_effort"]) != `"low"` || !strings.Contains(string(request["messages"]), "data:image/png;base64,aGk=") {
					t.Fatalf("request=%s", mustJSONForIntake(t, request))
				}
			} else {
				var reasoning struct{ Effort string }
				if err := json.Unmarshal(request["reasoning"], &reasoning); err != nil || reasoning.Effort != "low" || !strings.Contains(string(request["input"]), "data:image/png;base64,aGk=") {
					t.Fatalf("request=%s error=%v", mustJSONForIntake(t, request), err)
				}
			}
		})
	}
}
func TestIntakeProviderCallbacksRetainSourceSimpleOptions(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprintf("native=%t", native), func(t *testing.T) {
			services := newTestServices(t)
			const id = "source-options"
			var captured ai.StreamOptions
			respond := func(options ai.StreamOptions) *ai.AssistantMessageEventStream {
				captured = options
				stream := ai.NewAssistantMessageEventStream()
				stream.End(&ai.AssistantMessage{Provider: id, API: ai.APIOpenAICompletions, Model: "model", StopReason: ai.StopReasonStop})
				return stream
			}
			if native {
				model := &ai.Model{ID: "model", ProviderMeta: ai.ProviderMetadata{ProviderID: id, API: ai.APIOpenAICompletions}, Capabilities: ai.ModelCapabilities{ContextWindow: 128, MaxOutputTokens: 16}}
				callback := func(_ context.Context, _ *ai.Model, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
					return respond(options), nil
				}
				if err := services.ModelRuntime().RegisterNativeProvider(&ai.ModelsProvider{ID: id, Name: id, GetModels: func() ([]*ai.Model, error) { return []*ai.Model{model}, nil }, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
					return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "test"}}, nil
				}}}, Stream: callback, StreamSimple: callback}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := services.Registry().RegisterExtensionProvider(id, extension.ProviderConfig{API: ai.APIOpenAICompletions, BaseURL: "https://callback.invalid", APIKey: "test", Models: []extension.ProviderModelConfig{{ID: "model", Name: "model", ContextWindow: 128, MaxTokens: 16}}, StreamSimple: func(_ extension.Model, _ extension.AIContext, options extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
					return respond(options)
				}}); err != nil {
					t.Error(err)
				}
			}
			model := services.ModelRuntime().GetModel(id, "model")
			if model == nil {
				t.Fatal("missing model")
			}
			result := services.ModelRuntime().StreamSimple(t.Context(), model, ai.Context{}, ai.StreamOptions{MaxTokens: 98765, ReasoningEffort: "native-unmapped", TimeoutMs: new(0)}).Result()
			if result.StopReason != ai.StopReasonStop || captured.MaxTokens != 98765 || captured.Thinking != "" || captured.ReasoningEffort != "native-unmapped" || captured.TimeoutMs == nil || *captured.TimeoutMs != 0 {
				t.Fatalf("result=%#v options=%#v", result, captured)
			}
		})
	}
}

// recordingSummaryProvider records every request and answers "summary" with a fixed usage.
type recordingSummaryProvider struct {
	options []ai.StreamOptions
}

func (*recordingSummaryProvider) ID() string   { return "recording-summary" }
func (*recordingSummaryProvider) Close() error { return nil }
func (p *recordingSummaryProvider) Stream(_ context.Context, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.options = append(p.options, options)
	message := sessionTestMessage(p.ID(), "summary", ai.StopReasonStop, "")
	message.Usage = ai.Usage{Input: 21, Output: 5, TotalTokens: 26}
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}), nil
}

// models.ts:Models.streamSimple forwards source options to the provider callback. Only a stock API leaf lowers its budget and omitted reasoning.
func TestCallerOwnedProviderRetainsSimpleOptions(t *testing.T) {
	for _, maxTokens := range []int{0, 98765} {
		t.Run(fmt.Sprint(maxTokens), func(t *testing.T) {
			services := newTestServices(t)
			provider := &recordingSummaryProvider{}
			model := fakeModelWithProvider(provider)
			model.Capabilities.ContextWindow = 128
			model.Capabilities.MaxOutputTokens = 16
			options := ai.StreamOptions{MaxTokens: maxTokens, ReasoningEffort: "caller-effort", TimeoutMs: new(0)}
			result := services.ModelRuntime().CompleteSimple(t.Context(), model, ai.Context{}, options)
			if result.StopReason != ai.StopReasonStop || len(provider.options) != 1 {
				t.Fatalf("result=%#v calls=%d", result, len(provider.options))
			}
			got := provider.options[0]
			if got.MaxTokens != options.MaxTokens || got.Thinking != options.Thinking || got.ReasoningEffort != options.ReasoningEffort || got.TimeoutMs == nil || *got.TimeoutMs != 0 {
				t.Fatalf("caller options were lowered: %#v", got)
			}
		})
	}
}

func mustJSONForIntake(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
