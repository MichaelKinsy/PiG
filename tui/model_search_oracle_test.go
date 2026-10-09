package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/modes/interactive/model-search.ts

// getModelSearchText and getModelSelectorSearchText against pinned Pi, over ids with slashes (proxy providers), empty and absent
// names, unicode and spaces.
func TestModelSearchTextMatchesPi(t *testing.T) {
	type item struct {
		ID       string  `json:"id"`
		Provider string  `json:"provider"`
		Name     *string `json:"name"`
	}
	name := func(s string) *string { return &s }
	items := []item{
		{"gpt-5", "openai", name("GPT-5")},
		{"openai/gpt-5", "openrouter", name("OpenAI: GPT-5")},
		{"claude-opus-4-8", "anthropic", nil},
		{"claude-opus-4-8", "anthropic", name("")},
		{"family/model", "provider", name("Name With  Spaces")},
		{"日本語-モデル", "プロバイダ", name("名前")},
		{"", "", nil},
		{"a", "b", name("c")},
	}
	input, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/model_search.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][2]string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for i, it := range items {
		goItem := ModelSearchItem{ID: it.ID, Provider: it.Provider}
		if it.Name != nil {
			goItem.Name = *it.Name
		}
		if got := GetModelSearchText(goItem); got != expected[i][0] {
			t.Errorf("GetModelSearchText(%+v) = %q, Pi %q", it, got, expected[i][0])
		}
		if got := GetModelSelectorSearchText(goItem); got != expected[i][1] {
			t.Errorf("GetModelSelectorSearchText(%+v) = %q, Pi %q", it, got, expected[i][1])
		}
	}
}
