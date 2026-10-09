package inproc

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// runner.ts:1471 returns the shared options object after every handler, so an edit to a field other than sections and selectedTools (here appendSystemPrompt) still reaches the request.
func TestBeforeAgentStartReportsAnEditToAnyOptionsField(t *testing.T) {
	runner := NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {
		func(args ...any) (any, error) {
			extension.BeforeAgentStartOptions(args[1].(context.Context)).AppendSystemPrompt = "appended by a handler"
			return nil, nil
		},
	}}}}, t.TempDir())
	base := extension.BuildSystemPromptOptions{SelectedTools: []string{"read"}}
	result, err := runner.EmitBeforeAgentStart(t.Context(), "p", nil, base)
	if err != nil || result == nil || result.SystemPromptOptions == nil || result.SystemPromptOptions.AppendSystemPrompt != "appended by a handler" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if base.AppendSystemPrompt != "" {
		t.Fatalf("handler edit reached the caller's base options: %q", base.AppendSystemPrompt)
	}
}

// runner.ts:864 `shutdown(): void { this.shutdownHandler(); }` calls the bound handler before it returns.
func TestRunnerShutdownCallsTheBoundHandlerBeforeReturning(t *testing.T) {
	runner := NewRunner(nil, t.TempDir())
	called := false
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{Shutdown: func() { called = true }}, nil)
	runner.Shutdown()
	if !called {
		t.Fatal("Shutdown returned before the bound handler ran")
	}
}
