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
		newRunner: runner, tuiInst: tui.NewWithOutput(io.Discard, 80, 24), layout: tui.NewContainer(), agent: mustNewAgent(agent.AgentOptions{}),
		opts: InteractiveModeOptions{CWD: t.TempDir(), SystemPromptOptions: extension.BuildSystemPromptOptions{Cwd: "/prompt", ToolSnippets: prompts.DefaultToolSnippets()},
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

// runPromptHandle records the run interactive mode publishes to its Session.
type runPromptHandle struct {
	InteractiveSessionHandle
	published []*BeforeAgentStartRun
}

func (h *runPromptHandle) SetRunPrompt(run *BeforeAgentStartRun) {
	h.published = append(h.published, run)
}

// agent-session.ts prompt builds the run's options for every mode, and the Session records the run's sections in the
// transcript before the first request. Interactive mode prepares its own run, so it must hand it to the Session while the
// run lasts and take it back when the run ends; without it the extension sections (for example `mcp_servers`) never reach
// the transcript.
func TestInteractiveRunPromptIsPublishedToTheSessionForTheRunsDuration(t *testing.T) {
	handle := &runPromptHandle{}
	mode := &InteractiveMode{agent: mustNewAgent(agent.AgentOptions{}), opts: InteractiveModeOptions{SessionHandle: handle, SystemPrompt: "base"}}
	sections := ai.OrderedSections{{Name: "mcp_servers", Value: new("- mcp__docs (codemode)")}}
	run := BeforeAgentStartRun{Sections: sections}

	if err := mode.beginRunPrompt(3, run); err != nil {
		t.Fatal(err)
	}
	if len(handle.published) != 1 || handle.published[0] == nil || len(handle.published[0].Sections) != 1 || handle.published[0].Sections[0].Name != "mcp_servers" {
		t.Fatalf("published after begin = %+v, want the run with its sections", handle.published)
	}

	mode.endRunPrompt(2) // another run's end leaves this run alone
	if len(handle.published) != 1 {
		t.Fatalf("a stale end published %d times", len(handle.published))
	}
	mode.endRunPrompt(3)
	if len(handle.published) != 2 || handle.published[1] != nil {
		t.Fatalf("published after end = %+v, want the run ended with nil", handle.published)
	}
}
