package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi agent-session.ts:_bindExtensionCore returns the stored options object. Reading it again does not rebuild selectedTools or erase the command's edits.
func TestInteractiveCommandPromptOptionsRetainLiveMutations(t *testing.T) {
	cwd := t.TempDir()
	runner := inproc.NewRunner(nil, cwd)
	mode := &InteractiveMode{
		newRunner: runner,
		agent:     mustNewAgent(agent.AgentOptions{Tools: []agent.AgentTool{namedTool{name: "read"}}}),
		opts:      InteractiveModeOptions{CWD: cwd, SystemPromptOptions: extension.BuildSystemPromptOptions{Cwd: cwd, SelectedTools: []string{"read"}}},
	}
	mode.wireInprocContextActions()
	ctx := runner.CreateCommandContext()
	before, err := ctx.GetSystemPromptOptions()
	if err != nil {
		t.Fatal(err)
	}
	before.SelectedTools = append(before.SelectedTools, "command-edit")
	after, err := ctx.GetSystemPromptOptions()
	if err != nil {
		t.Fatal(err)
	}
	if before != after || !slices.Equal(after.SelectedTools, []string{"read", "command-edit"}) {
		t.Fatalf("getter discarded live options: same=%t selected=%v", before == after, after.SelectedTools)
	}
}
