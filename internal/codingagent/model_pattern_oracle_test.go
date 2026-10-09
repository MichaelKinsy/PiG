package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's parseModelPattern and findExactModelReferenceMatch (core/model-resolver.ts) run against the vendored Pi module for the same patterns and models.
func TestParseModelPatternMatchesPiOracle(t *testing.T) {
	type model struct {
		Provider string `json:"provider"`
		ID       string `json:"id"`
		Name     string `json:"name"`
	}
	models := []model{
		{"anthropic", "claude-sonnet-4-5", "Claude Sonnet 4.5"},
		{"anthropic", "claude-sonnet-4-5-20250929", "Claude Sonnet 4.5 (dated)"},
		{"anthropic", "claude-opus-latest", "Claude Opus"},
		{"anthropic", "claude-opus-4-1-20250805", "Claude Opus 4.1"},
		{"openrouter", "anthropic/claude-sonnet-4-5", "OR Sonnet"},
		{"openrouter", "qwen/qwen3:exacto", "Qwen exacto"},
		{"openrouter", "foo:bar:high", "Colon model"},
		{"openai", "gpt-5", "GPT-5"},
		{"openai", "gpt-5-20250807", "GPT-5 dated"},
		{"openai", "GPT-5-Mini", "Mini"},
		{"azure", "gpt-5", "Azure GPT-5"},
		{"local", "İstanbul", "Turkish İ"},
		{"local", "straße", "Eszett"},
		{"local", "model-a", ""},
		{"local", "model-b", ""},
		{"local", "a b", "space id"},
		{"google", "gemini-2.5-pro", "Gemini"},
		{"google", "gemini-2.5-pro-preview-05-06", "Gemini preview"},
	}
	patterns := []string{
		"", " ", "sonnet", "SONNET", "claude-sonnet-4-5", "anthropic/claude-sonnet-4-5", " Anthropic/Claude-Sonnet-4-5 ", "claude-sonnet-4-5:high",
		"claude-sonnet-4-5:off", "claude-sonnet-4-5:minimal", "claude-sonnet-4-5:max", "claude-sonnet-4-5:xhigh", "claude-sonnet-4-5:bogus", "claude-sonnet-4-5:HIGH",
		"opus", "opus:medium", "opus:bogus:high", "opus:high:bogus", "gpt-5", "gpt-5:low", "openai/gpt-5", "azure/gpt-5", "gpt", "mini", "qwen3:exacto", "qwen/qwen3:exacto:high",
		"foo:bar", "foo:bar:high", "foo:bar:high:low", "foo", "openrouter/foo:bar:high", "istanbul", "İSTANBUL", "i̇stanbul", "ISTANBUL", "STRASSE", "straße", "strasse",
		"model", "model-a", "MODEL-B", "a b", "a  b", "a\u00a0b", "\u00a0gpt-5\u00a0", "\ufeffgpt-5", "gemini-2.5-pro", "gemini-2.5-pro:high", "gemini", "preview",
		":", "::", "gpt-5:", ":high", "anthropic/", "/gpt-5", "anthropic/ claude-sonnet-4-5", "😀", "gpt-5😀:high", "20250929", "-latest", "latest",
	}
	type input struct {
		Models   []model  `json:"models"`
		Patterns []string `json:"patterns"`
		Fallback bool     `json:"fallback"`
	}
	type result struct {
		Model         string `json:"model"`
		ThinkingLevel string `json:"thinkingLevel"`
		Warning       string `json:"warning"`
		Exact         string `json:"exact"`
	}
	runtime := make([]RuntimeModel, len(models))
	for i, m := range models {
		runtime[i] = RuntimeModel{Provider: m.Provider, ID: m.ID, Name: m.Name}
	}
	for _, fallback := range []bool{true, false} {
		var want []result
		pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/model-resolver.js");
const ref = (m) => (m ? m.provider + "/" + m.id : "");
emit(input.patterns.map((p) => {
	const r = mod.parseModelPattern(p, input.models, { allowInvalidThinkingLevelFallback: input.fallback });
	return { model: ref(r.model), thinkingLevel: r.thinkingLevel ?? "", warning: r.warning ?? "", exact: ref(mod.findExactModelReferenceMatch(p, input.models)) };
}));`, input{models, patterns, fallback}, &want)
		for i, pattern := range patterns {
			got := ParseModelPattern(pattern, runtime, fallback)
			have := result{ThinkingLevel: got.ThinkingLevel, Warning: got.Warning}
			if got.Model != nil {
				have.Model = got.Model.Provider + "/" + got.Model.ID
			}
			if exact := FindExactModelReferenceMatch(pattern, runtime); exact != nil {
				have.Exact = exact.Provider + "/" + exact.ID
			}
			if have != want[i] {
				t.Errorf("fallback=%v %q: Go %+v, Pi %+v", fallback, pattern, have, want[i])
			}
		}
	}
}
