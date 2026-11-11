package providers

// pi: packages/ai/catalog/providers/opencode.ts
// pi: packages/ai/catalog/providers/opencode-go.ts

import (
	"testing"

	. "github.com/MichaelKinsy/PiG/internal/modelgen"
)

// Pi 9ee7586a9 (#10954): finalizeOpencodeModel calls enableClaudeMidConvoEffort when supportsClaudeMidConvoEffort(model.id), so OpenCode
// Zen and Go Claude 5.5 models, Opus 5 and Fable 5.1 send per-message effort and thinking.block_binding drop_block like the Anthropic
// provider. The golden test in cmd/gen-models holds the whole catalog to Pi's bytes; this one asserts the compat by value.
func TestOpencodeClaudeMidConvoEffort(t *testing.T) {
	messages := provBOpenCodeAPIs("messages", provBZen, "")
	specs := []provBSpec{
		{ID: "claude-opus-5", API: messages},
		{ID: "claude-opus-5-5", API: messages},
		{ID: "claude-sonnet-5-5", API: messages},
		{ID: "claude-haiku-5-5", API: messages},
		{ID: "claude-fable-5-1", API: messages},
		{ID: "claude-mythos-5-1", API: messages},
		// Models that do not accept per-message effort keep their compat and thinking levels.
		{ID: "claude-sonnet-4-5", API: messages},
		{ID: "claude-opus-4-6", API: messages},
		// The rule sits in the anthropic-messages branch: a Claude 5.5 id served over chat completions is untouched.
		{ID: "claude-haiku-5.5", API: provBOpenCodeAPIs("chat-completions", provBZen, "")},
	}
	sources := &CatalogSources{ModelsDev: provBModelsDev(t, map[string][]provBSpec{"opencode": specs, "opencode-go": specs})}
	for _, provider := range []string{"opencode", "opencode-go"} {
		t.Run(provider, func(t *testing.T) {
			catalog, err := provBBuild(t, provider, sources, false)
			if err != nil {
				t.Fatal(err)
			}
			// OpenCode Zen forwards mid-conversation system messages and adds that flag before the effort flag; Go does not.
			system := ""
			if provider == "opencode" {
				system = `,"supportsMidConvoSystemMessages":true`
			}
			effort := func(base string) string { return base + system + `,"supportsMidConvoEffort":true}` }
			provBCheck(t, provBModelsOf(catalog), []provBWant{
				{"claude-opus-5", "compat", effort(`{"forceAdaptiveThinking":true,"supportsTemperature":false`)},
				{"claude-opus-5-5", "compat", effort(`{"forceAdaptiveThinking":true,"supportsTemperature":false`)},
				{"claude-sonnet-5-5", "compat", effort(`{"forceAdaptiveThinking":true,"supportsTemperature":false`)},
				{"claude-haiku-5-5", "compat", effort(`{"forceAdaptiveThinking":true,"supportsTemperature":false`)},
				{"claude-fable-5-1", "compat", effort(`{"forceAdaptiveThinking":true`)},
				{"claude-mythos-5-1", "compat", effort(`{"forceAdaptiveThinking":true`)},
				// Models that cannot disable thinking alongside per-message effort keep "off"; the rest lose it.
				{"claude-opus-5", "thinkingLevelMap", `{"xhigh":"xhigh","max":"max"}`},
				{"claude-haiku-5-5", "thinkingLevelMap", `{"xhigh":"xhigh","max":"max"}`},
				{"claude-opus-5-5", "thinkingLevelMap", `{"xhigh":"xhigh","max":"max","off":null}`},
				{"claude-sonnet-5-5", "thinkingLevelMap", `{"xhigh":"xhigh","max":"max","off":null}`},
				{"claude-fable-5-1", "thinkingLevelMap", `{"off":null,"xhigh":"xhigh","max":"max"}`},
				{"claude-mythos-5-1", "thinkingLevelMap", `{"off":null}`},
				{"claude-sonnet-4-5", "thinkingLevelMap", "undefined"},
				{"claude-opus-4-6", "thinkingLevelMap", `{"max":"max"}`},
				{"claude-opus-4-6", "compat", `{"forceAdaptiveThinking":true}`},
				{"claude-haiku-5.5", "thinkingLevelMap", `{"xhigh":"xhigh","max":"max"}`},
			})
		})
	}
}
