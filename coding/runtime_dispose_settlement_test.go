package coding

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// disposeWaitTool runs until its context is cancelled, then fails 10 ms later like Pi's regression tool.
type disposeWaitTool struct{ started chan struct{} }

func (disposeWaitTool) Name() string                           { return "wait" }
func (disposeWaitTool) Label() string                          { return "Wait" }
func (disposeWaitTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (disposeWaitTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "wait", Description: "Wait until aborted", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (tool disposeWaitTool) Execute(ctx context.Context, _ string, _ json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	close(tool.started)
	<-ctx.Done()
	time.Sleep(10 * time.Millisecond)
	return agent.AgentToolResult{}, errors.New("Operation aborted")
}

// pi: test/suite/regressions/9124-runtime-dispose-tool-settlement.test.ts (Pi main 7a53c310a, #9126; D247). Closing the runtime
// during a tool settles the interrupted turn before the session is disconnected, and session_shutdown reaches extensions after it.
func TestRuntimeCloseDuringToolSettlesTheInterruptedTurnBeforeShutdown(t *testing.T) {
	tool := disposeWaitTool{started: make(chan struct{})}
	var h *recoveryHarness
	var mu sync.Mutex
	var rolesAtShutdown []string
	rolesOf := func() (roles []string) {
		for _, message := range h.session.Messages() {
			if message.Role() != "system" {
				roles = append(roles, message.Role())
			}
		}
		return roles
	}
	ext := extension.Extension{Path: "shutdown-order", Handlers: map[string][]extension.HandlerFn{"session_shutdown": {func(...any) (any, error) {
		mu.Lock()
		rolesAtShutdown = rolesOf()
		mu.Unlock()
		return nil, nil
	}}}}
	h = newRecoveryHarness(t, harnessOptions{emptySessionManager: true, tools: []agent.AgentTool{tool}, extension: ext}, fauxToolCall("wait"))
	runtime, err := NewAgentSessionRuntime(t.Context(), h.session, h.session.services, func(context.Context, CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		return CreateAgentSessionRuntimeResult{}, errors.New("runtime replacement is not used in this test")
	}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	prompt := make(chan error, 1)
	go func() { prompt <- h.session.Prompt(t.Context(), "run tool", nil) }()
	select {
	case <-tool.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the tool never started")
	}
	_ = runtime.Close()
	// #9124: Close itself must settle the turn; waiting for the prompt must not be necessary.
	want := []string{"user", "assistant", "toolResult", "assistant"}
	if got := rolesOf(); !slices.Equal(got, want) {
		t.Fatalf("messages after Close = %v, want %v", got, want)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(rolesAtShutdown, want) {
		t.Fatalf("session_shutdown saw messages %v, want the settled turn %v", rolesAtShutdown, want)
	}
	<-prompt
}

// pi: print-mode.ts:57-60 and agent-session-runtime.ts dispose (Pi 1.5.0, the pin): a termination signal's disposal emits session_shutdown at once and does
// not abort the run in flight. Print mode's signal hook holds the Agent at its next event (HoldAgentEvents) before it kills the tool, as Pi's one
// event loop stands still while the handlers run, so a run that the emit aborted could not settle and the emit would wait for it for ever.
func TestEmitQuitShutdownContextDoesNotAbortTheRunASignalHolds(t *testing.T) {
	tool := disposeWaitTool{started: make(chan struct{})}
	var h *recoveryHarness
	var mu sync.Mutex
	var shutdowns int
	var rolesAtShutdown []string
	ext := extension.Extension{Path: "shutdown-order", Handlers: map[string][]extension.HandlerFn{"session_shutdown": {func(...any) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		shutdowns++
		for _, message := range h.session.Messages() {
			if message.Role() != "system" {
				rolesAtShutdown = append(rolesAtShutdown, message.Role())
			}
		}
		return nil, nil
	}}}}
	h = newRecoveryHarness(t, harnessOptions{emptySessionManager: true, tools: []agent.AgentTool{tool}, extension: ext}, fauxToolCall("wait"))
	runtime, err := NewAgentSessionRuntime(t.Context(), h.session, h.session.services, func(context.Context, CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		return CreateAgentSessionRuntimeResult{}, errors.New("runtime replacement is not used in this test")
	}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	prompt := make(chan error, 1)
	go func() { prompt <- h.session.Prompt(t.Context(), "run tool", nil) }()
	select {
	case <-tool.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the tool never started")
	}
	release := h.session.HoldAgentEvents()
	defer release()
	emitted := make(chan struct{})
	go func() {
		runtime.EmitQuitShutdownContext(t.Context())
		close(emitted)
	}()
	select {
	case <-emitted:
	case <-time.After(5 * time.Second):
		// Ending the hold lets the aborted run settle, so the emit's wait and the prompt end before the test reports.
		release()
		<-emitted
		<-prompt
		t.Fatal("EmitQuitShutdownContext waited for the run that the signal's hold keeps from settling")
	}
	if h.session.AgentRunAbortRequested() {
		t.Error("EmitQuitShutdownContext aborted the run; Pi 1.5.0's dispose emits session_shutdown without an abort")
	}
	mu.Lock()
	gotShutdowns, gotRoles := shutdowns, slices.Clone(rolesAtShutdown)
	mu.Unlock()
	if want := []string{"user", "assistant"}; gotShutdowns != 1 || !slices.Equal(gotRoles, want) {
		t.Errorf("session_shutdown ran %d times and saw messages %v, want once on the unsettled turn %v", gotShutdowns, gotRoles, want)
	}
	release()
	_ = h.session.Abort(t.Context())
	<-prompt
}
