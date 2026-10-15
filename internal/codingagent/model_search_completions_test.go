package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Upstream /model argument completions fuzzy-filter on getModelSearchText,
// which carries the model name, so a name-only query completes the model.
func TestModelArgCompletionsSearchModelName(t *testing.T) {
	dir := t.TempDir()
	reg := NewModelRegistry(dir)
	if err := reg.RegisterExtensionProvider("zq-proxy", extension.ProviderConfig{
		BaseURL: "https://models.example/v1",
		APIKey:  "tok",
		API:     "openai-completions",
		Models:  []extension.ProviderModelConfig{{ID: "zq-1", Name: "Needle Model"}},
	}); err != nil {
		t.Error(err)
	}
	m := &InteractiveMode{opts: InteractiveModeOptions{ModelRegistry: reg, AgentDir: dir}}

	found := false
	for _, c := range m.modelArgCompletions("needle") {
		if c.Value == "zq-proxy/zq-1" {
			found = true
			if c.Label != "zq-1" || c.Description != "zq-proxy" {
				t.Fatalf("completion = %+v, want label zq-1 description zq-proxy", c)
			}
		}
	}
	if !found {
		t.Fatal("modelArgCompletions(needle) omitted zq-proxy/zq-1; search text must include the model name")
	}
}

// interactive-mode.ts:731-736: the /model argument completions list the scoped models when a scope is set, and every available model
// otherwise.
func TestModelArgCompletionsFollowTheModelScope(t *testing.T) {
	dir := t.TempDir()
	reg := NewModelRegistry(dir)
	if err := reg.RegisterExtensionProvider("zq-proxy", extension.ProviderConfig{
		BaseURL: "https://models.example/v1", APIKey: "tok", API: "openai-completions",
		Models: []extension.ProviderModelConfig{{ID: "zq-1", Name: "One"}, {ID: "zq-2", Name: "Two"}, {ID: "zq-3", Name: "Three"}},
	}); err != nil {
		t.Fatal(err)
	}
	m := &InteractiveMode{opts: InteractiveModeOptions{ModelRegistry: reg, AgentDir: dir}}
	values := func() []string {
		var out []string
		for _, item := range m.modelArgCompletions("zq-") {
			out = append(out, item.Value)
		}
		return out
	}
	if got := values(); len(got) != 3 {
		t.Fatalf("without a scope: %v, want the three zq models", got)
	}
	m.scopedModelIDs = []string{"zq-proxy/zq-2"}
	if got := values(); len(got) != 1 || got[0] != "zq-proxy/zq-2" {
		t.Fatalf("with a scope: %v, want only zq-proxy/zq-2", got)
	}
}
