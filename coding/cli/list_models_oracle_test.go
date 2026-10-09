package cli

// pi: packages/coding-agent/src/cli/list-models.ts

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type listModelsProbe struct {
	Models []listModelsModel `json:"models"`
	Search string            `json:"search"`
}

type listModelsModel struct {
	Provider      string   `json:"provider"`
	ID            string   `json:"id"`
	ContextWindow int      `json:"contextWindow"`
	MaxTokens     int      `json:"maxTokens"`
	Reasoning     bool     `json:"reasoning"`
	Input         []string `json:"input"`
}

// cli/list-models.ts listModels against pinned Pi over a stub runtime: provider/id sorting (String.prototype.localeCompare), the fuzzy search,
// token-count formatting, UTF-16 column padding and the no-match line.
func TestListModelsOutputMatchesPi(t *testing.T) {
	providers := []string{"anthropic", "openai", "Azure", "azure-openai", "OpenRouter", "google", "ollama", "Éclair", "zai", "x_y", "a-b", "a b", "日本", "😀p"}
	ids := []string{"gpt-4o", "GPT-4o", "gpt-4o-mini", "claude-3.5", "Claude-3.5", "claude_3", "o1", "o10", "o2", "model-é", "model-e", "Model", "model", "日本語", "😀-1", "a", "A", "ß", "ss", "x-1", "x-10", "x-2", "z"}
	counts := []int{0, 1, 999, 1000, 1500, 8192, 100000, 128000, 131072, 200000, 999999, 1000000, 1048576, 2000000, 1234567}
	r := rand.New(rand.NewPCG(7, 9))
	var probes []listModelsProbe
	searches := []string{"", "gpt", "open", "claude 3", "x1", "zzzz", `"q"`, "日本", "é", "a b", "o1", `back\slash`, "GPT", "oai", "azure gpt"}
	for range 150 {
		var models []listModelsModel
		seen := map[string]bool{}
		for range 1 + r.IntN(14) {
			m := listModelsModel{Provider: providers[r.IntN(len(providers))], ID: ids[r.IntN(len(ids))], ContextWindow: counts[r.IntN(len(counts))], MaxTokens: counts[r.IntN(len(counts))], Reasoning: r.IntN(2) == 0, Input: []string{"text"}}
			if r.IntN(2) == 0 {
				m.Input = append(m.Input, "image")
			}
			if seen[m.Provider+"\x00"+m.ID] {
				continue
			}
			seen[m.Provider+"\x00"+m.ID] = true
			models = append(models, m)
		}
		probes = append(probes, listModelsProbe{Models: models, Search: searches[r.IntN(len(searches))]})
	}
	probes = append(probes, listModelsProbe{Models: nil, Search: ""}, listModelsProbe{Models: nil, Search: "x"})
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/list_models.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, probe := range probes {
		entries := make([]codingagent.ModelEntry, len(probe.Models))
		for j, m := range probe.Models {
			entries[j] = codingagent.ModelEntry{ProviderID: m.Provider, ModelID: m.ID, ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens, Reasoning: m.Reasoning, Input: m.Input}
		}
		got := modelListLines(entries, probe.Search)
		if len(probe.Models) == 0 {
			// The message ends with documentation paths of the running installation, which differ by installation.
			if len(got) != 1 || len(expected[i]) != 1 || !strings.HasPrefix(got[0], "No models available. Use /login") || !strings.HasPrefix(expected[i][0], "No models available. Use /login") {
				t.Errorf("no models: Pig %q, Pi %q", got, expected[i])
			}
			continue
		}
		if !reflect.DeepEqual(got, expected[i]) {
			if failures++; failures <= 3 {
				t.Errorf("search %q over %+v:\n  Pig %q\n  Pi  %q", probe.Search, probe.Models, got, expected[i])
			}
		}
	}
	if failures > 3 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
