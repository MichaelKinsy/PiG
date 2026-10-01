package codingagent

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// /session lists the cost per model only when there is more than one entry, or the single entry names a model other than
// the selected one: "A single entry repeats the total, unless it names a model other than the selected one"
// (.upstream/v0.99.1/packages/coding-agent/src/modes/interactive/interactive-mode.ts, handleSessionCommand renderInfo).
// A virtual model's routed physical model is such a case: the usage is keyed by the response model.
func sessionCostBreakdownOutput(t *testing.T, responseModel string, selected func() string) string {
	t.Helper()
	sess := NewSession("cost-session", "/tmp")
	entry := MessageEntry{SessionEntryBase: SessionEntryBase{Type: "message", ID: "a1", Timestamp: "2025-01-01T00:00:00Z"}, Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role: "assistant", Provider: "openai", ModelID: "virtual", ResponseModel: responseModel,
		Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}},
		Usage:   &ai.Usage{Input: 10, Output: 5, Cost: ai.UsageCost{Total: 0.5}},
	}}}
	if err := sess.AppendEntry(entry); err != nil {
		t.Fatal(err)
	}
	sc, out, _, _ := newTestSlashContext()
	sc.CurrentSession = func() *Session { return sess }
	sc.SelectedModelKey = selected
	if err := sessionHandler(sc); err != nil {
		t.Fatal(err)
	}
	return regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`).ReplaceAllString(out.String(), "")
}

func TestSessionCostOmitsTheBreakdownOfTheSelectedModel(t *testing.T) {
	plain := sessionCostBreakdownOutput(t, "", func() string { return "openai/virtual" })
	if !strings.Contains(plain, "Total: $0.500") {
		t.Fatalf("no cost total: %s", plain)
	}
	if strings.Contains(plain, "openai/virtual:") {
		t.Fatalf("a single entry for the selected model repeated the total: %s", plain)
	}
}

func TestSessionCostNamesAModelOtherThanTheSelectedOne(t *testing.T) {
	plain := sessionCostBreakdownOutput(t, "gpt-physical", func() string { return "openai/virtual" })
	if !strings.Contains(plain, "openai/gpt-physical: $0.500 (15 tokens)") {
		t.Fatalf("the routed model's cost is missing: %s", plain)
	}
}

// interactive-mode.ts: selectedModelKey is `${model?.provider}/${model?.id}`, so a session without a model matches no entry.
func TestSessionCostWithoutASelectedModelShowsTheBreakdown(t *testing.T) {
	for name, selected := range map[string]func() string{"no accessor": nil, "no model": func() string { return "undefined/undefined" }} {
		t.Run(name, func(t *testing.T) {
			plain := sessionCostBreakdownOutput(t, "", selected)
			if !strings.Contains(plain, "openai/virtual: $0.500 (15 tokens)") {
				t.Fatalf("breakdown missing: %s", plain)
			}
		})
	}
}

// The interactive accessor keys the selected model by its declared provider, as the usage entries are keyed; a model
// described only by catalog metadata has no transport Provider, and it still matches its own single entry.
func TestSessionCostSelectedModelKeyUsesTheDeclaredProvider(t *testing.T) {
	m := &InteractiveMode{}
	if got := m.buildSlashContext(context.Background()).SelectedModelKey(); got != "undefined/undefined" {
		t.Fatalf("key without a model = %q, want undefined/undefined", got)
	}
	m.opts.Model = &ai.Model{ID: "virtual", ProviderMeta: ai.ProviderMetadata{ProviderID: "openai"}}
	selected := m.buildSlashContext(context.Background()).SelectedModelKey
	if got := selected(); got != "openai/virtual" {
		t.Fatalf("selected model key = %q, want openai/virtual", got)
	}
	if plain := sessionCostBreakdownOutput(t, "", selected); strings.Contains(plain, "openai/virtual:") {
		t.Fatalf("a single entry for the selected model repeated the total: %s", plain)
	}
}
