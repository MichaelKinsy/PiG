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
	reg.RegisterProvider("zq-proxy", extension.ProviderConfig{
		BaseURL: "https://models.example/v1",
		APIKey:  "tok",
		API:     "openai-completions",
		Models:  []extension.ProviderModelConfig{{ID: "zq-1", Name: "Needle Model"}},
	})
	m := &InteractiveMode{opts: InteractiveOptions{ModelRegistry: reg, AgentDir: dir}}

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
