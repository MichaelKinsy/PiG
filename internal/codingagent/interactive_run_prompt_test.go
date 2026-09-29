package codingagent

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/tui"
)

// The owner may be stalled while a finished run publishes idle. Its prompt must already be cleared before a replacement is allowed to observe that publication.
func TestRunPromptEndsBeforeIdlePublication(t *testing.T) {
	mode, ctx, cancel := newLifecycleMode(t)
	mode.opts.SystemPrompt = "base"
	settled := make(chan struct{})
	mode.newRunner = inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{"agent_settled": {func(...any) (any, error) { close(settled); return nil, nil }}}}}, t.TempDir())
	if err := mode.beginRunPrompt(1, BeforeAgentStartRun{SystemPrompt: new("run")}); err != nil {
		t.Fatal(err)
	}
	for range cap(mode.uiTaskCh) {
		mode.uiTaskCh <- func() {}
	}
	mode.runTurn(ctx, "", func(context.Context) ([]agent.AgentMessage, error) { return nil, nil })
	mode.queueMu.Lock()
	idle := mode.turnSettled
	mode.queueMu.Unlock()
	if idle != nil {
		select {
		case <-idle:
		case <-time.After(5 * time.Second):
			t.Fatal("run did not publish idle")
		}
	}
	if got := mode.currentSystemPrompt(); got != "base" {
		t.Errorf("idle published before ending prompt: %q", got)
	}
	cancel()
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("run cleanup did not complete")
	}
}

// agent-session.ts:700-709 rebuilds each later turn of a run from the run options with the live active tools, and :1485 drops the run options when the run ends. A setActiveTools call during or after a run therefore changes the prompt the provider receives next.
func TestInteractiveSetActiveToolsRebuildsForcedPrompt(t *testing.T) {
	runner := inproc.NewRunner(nil, t.TempDir())
	mode := &InteractiveMode{
		newRunner: runner, tuiInst: tui.NewWithOutput(io.Discard, 80, 24), layout: tui.NewContainer(), agent: agent.NewAgent(agent.AgentOptions{}),
		opts: InteractiveOptions{CWD: t.TempDir(), SystemPromptOptions: extension.BuildSystemPromptOptions{Cwd: "/prompt", ToolSnippets: prompts.DefaultToolSnippets()},
			ActiveBuiltinTools: map[string]struct{}{"read": {}, "bash": {}},
		},
	}
	mode.wireInprocContextActions()
	ctx := runner.CreateCommandContext()
	ctx.SetActiveTools([]string{"read", "bash"})
	forced := func() string {
		t.Helper()
		prompt, present := mode.agent.SystemPromptSnapshot()
		if !present || prompt != mode.currentSystemPrompt() {
			t.Fatalf("forced prompt %q (present=%t) differs from the reported prompt %q", prompt, present, mode.currentSystemPrompt())
		}
		return prompt
	}
	const section = "<plan_mode>\nPlan only.\n</plan_mode>"
	sections := ai.OrderedSections{{Name: "plan_mode", Value: new("Plan only.")}}
	options := *mode.currentSystemPromptOptions()
	options.Sections = &sections
	run, err := ResolveBeforeAgentStartRun(*mode.currentSystemPromptOptions(), &extension.BeforeAgentStartCombinedResult{SystemPromptOptions: &options})
	if err != nil {
		t.Fatal(err)
	}
	if err := mode.beginRunPrompt(7, run); err != nil {
		t.Fatal(err)
	}
	if prompt := forced(); !strings.Contains(prompt, section) || !strings.Contains(prompt, "- bash:") {
		t.Fatalf("run prompt = %q", prompt)
	}
	ctx.SetActiveTools([]string{"read"})
	if prompt := forced(); !strings.Contains(prompt, section) || !strings.Contains(prompt, "- read:") || strings.Contains(prompt, "- bash:") {
		t.Fatalf("run prompt after setActiveTools = %q", prompt)
	}
	mode.endRunPrompt(6)
	if prompt := forced(); !strings.Contains(prompt, section) {
		t.Fatalf("a stale run ended the current run's prompt: %q", prompt)
	}
	mode.endRunPrompt(7)
	if prompt := forced(); strings.Contains(prompt, section) || !strings.Contains(prompt, "- read:") || strings.Contains(prompt, "- bash:") {
		t.Fatalf("base prompt after the run = %q", prompt)
	}
	ctx.SetActiveTools([]string{"bash"})
	if prompt := forced(); !strings.Contains(prompt, "- bash:") || strings.Contains(prompt, "- read:") {
		t.Fatalf("base prompt after setActiveTools = %q", prompt)
	}
}
