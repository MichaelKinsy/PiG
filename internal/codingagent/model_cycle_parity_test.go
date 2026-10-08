package codingagent

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type cycleTestProvider struct{ id string }

func (p cycleTestProvider) ID() string   { return p.id }
func (p cycleTestProvider) Close() error { return nil }
func (p cycleTestProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return completedTestStream(p.id), nil
}

func TestInteractiveCycleUsesSessionSelections(t *testing.T) {
	current := &ai.Model{ID: "gpt-4-turbo", ProviderMeta: ai.ProviderMetadata{ProviderID: "openai"}}
	custom := &ai.Model{ID: "custom-model", DisplayName: "Custom Model", ProviderMeta: ai.ProviderMetadata{ProviderID: "custom"}}
	gpt4 := &ai.Model{ID: "gpt-4", DisplayName: "GPT-4", ProviderMeta: ai.ProviderMetadata{ProviderID: "openai"}}
	handle := &recordingCompactHandle{
		agent:        agent.NewAgent(agent.AgentOptions{Model: current}),
		scopedModels: []extension.ScopedModel{{Model: custom}, {Model: gpt4}, {Model: current}},
		cycleResults: []*ModelCycleResult{{Model: custom}, {Model: gpt4}, {Model: current}, {Model: custom}},
	}
	m := &InteractiveMode{opts: InteractiveOptions{
		Model:         current,
		SessionHandle: handle,
		ModelBuilder:  func(string) (*ai.Model, error) { t.Fatal("interactive cycle rebuilt the model list"); return nil, nil },
	}}
	m.statusLine = NewStatusLine(current, "test", nil)

	for _, result := range handle.cycleResults {
		m.cycleModel(true)
		if m.opts.Model != result.Model {
			t.Fatalf("interactive model = %v, want Session selection %v", m.opts.Model, result.Model)
		}
	}
	got := make([]string, 0, len(handle.cycled))
	for _, model := range handle.cycled {
		got = append(got, modelSpec(model))
	}
	want := []string{"custom/custom-model", "openai/gpt-4", "openai/gpt-4-turbo", "custom/custom-model"}
	if !slices.Equal(got, want) {
		t.Fatalf("cycled models = %v, want Session selections %v", got, want)
	}
	if wantDirections := []string{"forward", "forward", "forward", "forward"}; !slices.Equal(handle.cycleDirections, wantDirections) {
		t.Fatalf("cycle directions = %v, want %v", handle.cycleDirections, wantDirections)
	}
}

func TestInteractiveCycleStatusMatchesSessionResult(t *testing.T) {
	reasoning := &ai.Model{
		ID:           "reasoning-model",
		DisplayName:  "Reasoning Model",
		Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingHigh},
		ProviderMeta: ai.ProviderMetadata{ProviderID: "custom"},
	}
	for _, tc := range []struct {
		name       string
		scoped     []extension.ScopedModel
		result     *ModelCycleResult
		forward    bool
		wantStatus string
	}{
		{name: "one available", wantStatus: "Only one model available", forward: true},
		{name: "one in scope", scoped: []extension.ScopedModel{{Model: reasoning}}, wantStatus: "Only one model in scope", forward: true},
		{name: "reasoning level", result: &ModelCycleResult{Model: reasoning, ThinkingLevel: ai.ThinkingHigh, IsScoped: true}, wantStatus: "Switched to Reasoning Model (thinking: high)"},
		{name: "reasoning off", result: &ModelCycleResult{Model: reasoning, ThinkingLevel: ai.ThinkingOff}, wantStatus: "Switched to Reasoning Model"},
		{name: "non-reasoning id fallback", result: &ModelCycleResult{Model: &ai.Model{ID: "plain"}, ThinkingLevel: ai.ThinkingHigh}, wantStatus: "Switched to plain"},
		{name: "reasoning metadata", result: &ModelCycleResult{Model: &ai.Model{ID: "metadata", ProviderMeta: ai.ProviderMetadata{Reasoning: true}}, ThinkingLevel: ai.ThinkingLow}, wantStatus: "Switched to metadata (thinking: low)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handle := &recordingCompactHandle{scopedModels: tc.scoped, cycleResults: []*ModelCycleResult{tc.result}}
			m := &InteractiveMode{opts: InteractiveOptions{SessionHandle: handle}}
			m.statusLine = NewStatusLine(nil, "test", nil)
			var status string
			m.statusLine.SetStatusHook(func(message string) { status = message })

			m.cycleModel(tc.forward)

			if status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", status, tc.wantStatus)
			}
			wantDirection := "backward"
			if tc.forward {
				wantDirection = "forward"
			}
			if !slices.Equal(handle.cycleDirections, []string{wantDirection}) {
				t.Fatalf("cycle directions = %v, want [%s]", handle.cycleDirections, wantDirection)
			}
		})
	}
}
