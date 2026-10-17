package codingagent

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi agent-session.ts setActiveToolsByName rebuilds the prompt in requested
// order and never retains a built-in omitted from that selection.
func TestInteractiveToolPromptActivationOrder(t *testing.T) {
	runner := inproc.NewRunner([]extension.Extension{{Name: "prompt", Tools: map[string]extension.RegisteredTool{
		"alpha": {Definition: extension.ToolDefinition{Name: "alpha", PromptSnippet: "Alpha summary", PromptGuidelines: []string{"alpha rule"}}},
		"zeta":  {Definition: extension.ToolDefinition{Name: "zeta", PromptSnippet: " Zeta\n summary ", PromptGuidelines: []string{"zeta rule"}}},
	}}}, t.TempDir())
	mode := &InteractiveMode{
		newRunner: runner, tuiInst: tui.NewWithOutput(io.Discard, 80, 24), layout: tui.NewContainer(), agent: mustNewAgent(agent.AgentOptions{}),
		opts: InteractiveModeOptions{CWD: t.TempDir(), SystemPromptOptions: extension.BuildSystemPromptOptions{Cwd: "/prompt", ToolSnippets: prompts.DefaultToolSnippets()},
			ActiveBuiltinTools: map[string]struct{}{"read": {}},
			BridgeExtensionTools: func(definitions []extension.RegisteredTool) ([]agent.AgentTool, []error) {
				var tools []agent.AgentTool
				for _, def := range definitions {
					tools = append(tools, namedTool{name: def.Definition.Name})
				}
				return tools, nil
			},
		},
	}
	mode.wireInprocContextActions()
	ctx := runner.CreateCommandContext()
	ctx.SetActiveTools([]string{"zeta", "alpha"})
	if got := ctx.GetActiveTools(); !slices.Equal(got, []string{"zeta", "alpha"}) {
		t.Fatalf("active tools = %v", got)
	}
	prompt := mode.currentSystemPrompt()
	if !strings.Contains(prompt, "- zeta: Zeta summary\n- alpha: Alpha summary") || !strings.Contains(prompt, "- zeta rule\n- alpha rule") || strings.Contains(prompt, "- read:") {
		t.Fatalf("prompt = %s", prompt)
	}
	ctx.SetActiveTools([]string{})
	if prompt := mode.currentSystemPrompt(); !strings.Contains(prompt, "<tools>\n(none)\n") || strings.Contains(prompt, "zeta rule") || strings.Contains(prompt, "alpha rule") {
		t.Fatalf("inactive contributions = %s", prompt)
	}
}
