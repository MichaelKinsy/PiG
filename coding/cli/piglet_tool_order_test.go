package cli

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

// With a Piglet active, the provider request lists the tools the session selected, in the order it selected them. Pi
// declares agent.state.tools, which _applyToolLoadout builds by walking setActiveTools' names in argument order
// (agent-session.ts:1561-1565, _applyToolLoadout), and starts with the default tools plus the extension's tools. The Piglet scope only
// narrows that selection (#169), so it keeps the set and the order: an extension's setActiveTools(["read","grep","bash"])
// reaches each provider API as [read, grep, bash] in every mode, not in tool-registry order, and without a selection
// the default tools stay the default tools, not every registered built-in.
func TestPigletKeepsTheSessionsToolSelectionInEveryMode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Node extensions")
	}
	t.Parallel()
	for _, api := range []struct {
		name     string
		provider func(*testing.T) *scopeProvider
	}{
		{"openai-completions", newScopeProvider},
		{"anthropic-messages", newAnthropicScopeProvider},
	} {
		t.Run(api.name, func(t *testing.T) {
			for _, mode := range []string{"print", "json", "rpc", "interactive"} {
				t.Run(mode, func(t *testing.T) {
					for _, selection := range []struct {
						name, extension string
						want            []string
					}{
						{"setActiveTools", `  pi.on("session_start", () => { pi.setActiveTools(["read", "grep", "bash"]); });` + "\n", []string{"read", "grep", "bash"}},
						{"default", "", []string{"read", "bash", "edit", "write", "scoped_tool"}},
					} {
						t.Run(selection.name, func(t *testing.T) {
							provider := api.provider(t)
							home, piglet := scopeFixture(t, provider, "")
							writeStartupFixtureFile(t, filepath.Join(home, "agent", "models.json"), fmt.Sprintf(`{"providers":{"fake":{"baseUrl":%q,"api":%q,"apiKey":"synthetic-test-key","models":[{"id":"fake","name":"Fake","reasoning":false,"input":["text"],"contextWindow":128000,"maxTokens":1024,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`, provider.url, api.name))
							writeStartupFixtureFile(t, filepath.Join(piglet, "scoped.mjs"), `export default function (pi) {
  pi.registerTool({ name: "scoped_tool", label: "Scoped", description: "scoped", parameters: { type: "object", properties: {} }, execute: async () => ({ content: [{ type: "text", text: "ok" }] }) });
`+selection.extension+`}
`)
							if names := firstRequestTools(t, provider, home, piglet, mode); !slices.Equal(names, selection.want) {
								t.Errorf("offered %v, want %v", names, selection.want)
							}
						})
					}
				})
			}
		})
	}
}
