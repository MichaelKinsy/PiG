// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// unknownWindowUsage is the usage every scripted reply of these tests carries: 1010 tokens, over the threshold of a 2000-token window under
// the default reserve (a window of 0 is unknown, not exhausted: D660).
var unknownWindowUsage = ai.Usage{Input: 1000, Output: 10, TotalTokens: 1010}

// TestCheckCompactionDecisionUnknownWindow runs the post-reply decision on its own: a spy stands in for the admitted compaction, so the table
// states what the decision is for each window. A window of 0 or below is unknown and never reaches the threshold comparison, whatever the
// reply's usage; a known window is compared against it as Pi's shouldCompact does.
func TestCheckCompactionDecisionUnknownWindow(t *testing.T) {
	cases := []struct {
		name      string
		window    int
		usage     *ai.Usage
		wantCalls int
	}{
		{"window 0 with usage is unknown", 0, &unknownWindowUsage, 0},
		{"negative window with usage is unknown", -1, &unknownWindowUsage, 0},
		{"large negative window with usage is unknown", -1_000_000, &unknownWindowUsage, 0},
		{"window 0 without usage is unknown", 0, &ai.Usage{}, 0},
		{"known window below the usage threshold compacts", 2000, &unknownWindowUsage, 1},
		{"known window above the usage threshold does not", 1_000_000, &unknownWindowUsage, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := fakeModelWithProvider(&scriptedProvider{})
			model.Capabilities.ContextWindow = tc.window
			model.Capabilities.MaxOutputTokens = 8192
			s, err := NewSession(newTestServicesSmallKeep(t), SessionOptions{Model: model, NoSession: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			message := &agent.AssistantMessage{
				Role:       "assistant",
				Content:    []ai.AssistantContentBlock{ai.TextContent{Text: "4"}},
				Provider:   model.Provider.ID(),
				ModelID:    model.ID,
				StopReason: ai.StopReasonStop,
				Usage:      tc.usage,
				Timestamp:  time.Now().UnixMilli(),
			}
			// A window below 0 also makes Pi's overflow check (ai.IsContextOverflow, unchanged here) fire on any usage, so only the threshold
			// requests are counted: the guard is the threshold's.
			var reasons []string
			compact := func(_ context.Context, reason string, _ bool) (bool, error) {
				if reason == "threshold" {
					reasons = append(reasons, reason)
				}
				return false, nil
			}
			if _, err := s.checkCompactionDecision(t.Context(), message, false, compact); err != nil {
				t.Fatal(err)
			}
			if len(reasons) != tc.wantCalls {
				t.Fatalf("window %d: threshold compaction requested %d times (%v), want %d", tc.window, len(reasons), reasons, tc.wantCalls)
			}
			if tc.wantCalls == 1 && reasons[0] != "threshold" {
				t.Fatalf("reason = %q, want threshold", reasons[0])
			}
		})
	}
}

// TestUnknownWindowAfterReplyAcrossProviderShapes sends one prompt through the session's post-reply path for models of three shapes. A
// model with no window (a custom OpenAI-compatible model an embedder builds, a model an extension registers without a contextWindow) starts
// no compaction; the same shape with a known small window starts one; a catalog model with its real window starts none.
func TestUnknownWindowAfterReplyAcrossProviderShapes(t *testing.T) {
	const smallWindow = 2000
	type shape struct {
		name string
		// build returns the model with the window the shape has in practice.
		build func(t *testing.T, services *AgentSessionServices, provider ai.Provider) *ai.Model
		// unknown is whether build must produce a window of 0: a change of the shape's premise fails the test instead of passing silently.
		unknown bool
	}
	shapes := []shape{
		{"custom OpenAI-compatible API-key model without a window", func(t *testing.T, _ *AgentSessionServices, provider ai.Provider) *ai.Model {
			return &ai.Model{
				ID: "custom-1", DisplayName: "custom-1", Provider: provider,
				ProviderMeta: ai.ProviderMetadata{API: ai.APIOpenAICompletions, BaseURL: "https://llm.example.invalid/v1"},
				Capabilities: ai.ModelCapabilities{MaxOutputTokens: 8192},
			}
		}, true},
		{"extension-registered model without a window", func(t *testing.T, services *AgentSessionServices, provider ai.Provider) *ai.Model {
			config := extension.ProviderConfig{BaseURL: "https://llm.example.invalid/v1", API: ai.APIOpenAICompletions, APIKey: "test-key", Models: []extension.ProviderModelConfig{{ID: "no-window", Name: "No window", Input: []string{"text"}}}}
			if err := services.Registry().RegisterExtensionProvider("proxy", config); err != nil {
				t.Fatal(err)
			}
			built, err := BuildModel("proxy/no-window", services)
			if err != nil {
				t.Fatal(err)
			}
			model := *built
			model.Provider = provider
			model.Capabilities.MaxOutputTokens = 8192
			return &model
		}, true},
		{"built-in catalog model with its window", func(t *testing.T, _ *AgentSessionServices, provider ai.Provider) *ai.Model {
			entry, ok := ai.LookupModelExact("azure/gpt-6-astra")
			if !ok {
				t.Fatal("the generated catalog no longer has azure/gpt-6-astra: pick another catalogued model")
			}
			model := entry.ToModel()
			model.Provider = provider
			return model
		}, false},
	}
	run := func(t *testing.T, services *AgentSessionServices, provider *scriptedProvider, model *ai.Model) (compactions, compactionEvents int) {
		t.Helper()
		provider.responses = append(provider.responses, func([]ai.Message) *ai.AssistantMessage {
			return &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "4"}}, Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Usage: unknownWindowUsage, Timestamp: time.Now().UnixMilli()}
		})
		for range 4 {
			provider.responses = append(provider.responses, fauxReply("complete summary", ai.StopReasonStop, 0))
		}
		s, err := NewSession(services, SessionOptions{Model: model, NoSession: true})
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Events()
		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
		if _, err := s.Send(t.Context(), "What is 2+2? Reply with just the number."); err != nil {
			t.Fatal(err)
		}
		for _, event := range drainEvents(t, s) {
			switch event.(type) {
			case agent.CompactionStartEvent, agent.CompactionEndEvent:
				compactionEvents++
			}
		}
		for _, entry := range s.inner.Load().GetEntries() {
			if entry.Base().Type == "compaction" {
				compactions++
			}
		}
		return compactions, compactionEvents
	}
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			t.Run("window as the shape has it", func(t *testing.T) {
				provider := &scriptedProvider{}
				services := newTestServicesSmallKeep(t)
				model := sh.build(t, services, provider)
				if got := model.Capabilities.ContextWindow; (got <= 0) != sh.unknown {
					t.Fatalf("shape premise: window = %d, unknown = %v", got, sh.unknown)
				}
				compactions, events := run(t, services, provider, model)
				if compactions != 0 || events != 0 {
					t.Fatalf("window %d: compactions=%d events=%d requests=%d, want none", model.Capabilities.ContextWindow, compactions, events, provider.callCount())
				}
				if provider.callCount() != 1 {
					t.Fatalf("requests = %d, want only the prompt reply", provider.callCount())
				}
			})
			t.Run("known small window", func(t *testing.T) {
				provider := &scriptedProvider{}
				services := newTestServicesSmallKeep(t)
				model := sh.build(t, services, provider)
				model.Capabilities.ContextWindow = smallWindow
				compactions, events := run(t, services, provider, model)
				if compactions != 1 || events == 0 {
					t.Fatalf("window %d: compactions=%d events=%d requests=%d, want one compaction", smallWindow, compactions, events, provider.callCount())
				}
				if provider.callCount() < 2 {
					t.Fatalf("requests = %d, want the prompt reply and a summariser request", provider.callCount())
				}
			})
		})
	}
}
