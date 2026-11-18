package subprocess

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The agent loop answers "Operation aborted" without calling execute at its three gates (agent-loop.ts:630-635 the parallel batch's call, 748-753 after the
// before hooks, 767-772 before the call is prepared; agent/tool_execution.go runPreparedToolCall, prepareToolCall's hook loop and its entry), so an
// extension tool is never entered by a call the loop had already seen aborted. Only an abort that lands after the last gate reaches the extension with its
// signal aborted (D111). These tests run the real loop over a subprocess extension's tool.

// gateTool is the extension tool of a loop run: Execute runs the host's dispatch, after enter, which a test uses to abort after the loop's last gate.
type gateTool struct {
	execute extension.ToolExecuteFunc
	enter   func()
}

func (gateTool) Name() string                           { return "probe" }
func (gateTool) Label() string                          { return "probe" }
func (gateTool) Schema() ai.ToolSchema                  { return ai.ToolSchema{Name: "probe"} }
func (gateTool) ExecutionMode() agent.ToolExecutionMode { return "" }
func (t gateTool) Execute(ctx context.Context, id string, params json.RawMessage, onUpdate agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	if t.enter != nil {
		t.enter()
	}
	return t.execute(ctx, id, params, onUpdate)
}

type gateRun struct {
	agent *agent.Agent
	mu    sync.Mutex
	ends  map[string]agent.ToolExecutionEndEvent
	done  chan error
}

// startGateRun starts the run into run, which the caller made before it built the closures that abort it, so no closure reads the variable while it is assigned. It starts a run whose first assistant message calls the extension's tool once per id, in the given batch mode.
func startGateRun(t *testing.T, run *gateRun, mode agent.ToolExecutionMode, ids []string, tool agent.AgentTool, before agent.BeforeToolCallFunc, abortAfterAssistant bool) *gateRun {
	t.Helper()
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	blocks := make([]ai.FauxContentBlock, len(ids))
	for i, id := range ids {
		blocks[i] = ai.FauxToolCall("probe", map[string]any{}, &ai.FauxToolCallOptions{ID: id})
	}
	provider.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: blocks, StopReason: string(ai.StopReasonToolUse)}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}}),
	})
	run.ends, run.done = map[string]agent.ToolExecutionEndEvent{}, make(chan error, 1)
	a, err := agent.NewAgent(agent.AgentOptions{
		Model:          &ai.Model{ID: "faux-1", Provider: provider, ProviderMeta: ai.ProviderMetadata{API: "faux", ProviderID: "faux"}},
		Tools:          []agent.AgentTool{tool},
		ToolExecution:  mode,
		BeforeToolCall: before,
		DefaultStreamFn: func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return model.Provider.Stream(ctx, transcript, options)
		},
		OnEvent: func(event agent.AgentEvent) {
			switch event := event.(type) {
			case agent.ToolExecutionEndEvent:
				run.mu.Lock()
				run.ends[event.ToolCallID] = event
				run.mu.Unlock()
			case agent.MessageEndEvent:
				if abortAfterAssistant && event.Message.Assistant != nil && event.Message.Assistant.StopReason == ai.StopReasonToolUse {
					run.agent.Abort()
				}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	run.agent = a
	go func() { run.done <- a.Prompt(t.Context(), "go") }()
	return run
}

func (r *gateRun) wait(t *testing.T) {
	t.Helper()
	select {
	case err := <-r.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the run never ended")
	}
}

func (r *gateRun) end(t *testing.T, id string) (text string, isError bool) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	event, ok := r.ends[id]
	if !ok {
		t.Fatalf("call %s has no tool_execution_end", id)
	}
	return toolResultText(extension.AgentToolResult(event.Result)), event.IsError
}

// expectSilence fails when the extension's process receives any frame within the wait: not the state the tool runs against, not the request.
func (r *endedCallRig) expectSilence(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case env := <-r.frames:
		t.Fatalf("the extension was sent a frame: %+v", env)
	case <-time.After(within):
	}
}

// (a) The run is aborted before a gate of the loop: the call's result is "Operation aborted", the extension's execute is not called, and nothing is
// written to its process. Each case reaches the extension's tool only through the gate it names, so removing that gate sends the call.
func TestAbortedRunAnswersAtTheLoopsGatesWithoutCallingTheExtension(t *testing.T) {
	cases := []struct {
		name  string
		mode  agent.ToolExecutionMode
		ids   []string
		build func(abort func()) (before agent.BeforeToolCallFunc, afterAssistant bool)
	}{
		{"abort before the batch starts: the entry gate of prepareToolCall", agent.ToolModeSequential, []string{"tc-1", "tc-2"}, func(func()) (agent.BeforeToolCallFunc, bool) { return nil, true }},
		{"abort during the before hook: the gate after the hooks", agent.ToolModeSequential, []string{"tc-1"}, func(abort func()) (agent.BeforeToolCallFunc, bool) {
			return func(context.Context, agent.BeforeToolCallContext) *agent.BeforeToolCallResult { abort(); return nil }, false
		}},
		{"abort while a later call is prepared: the parallel batch's call gate", agent.ToolModeParallel, []string{"tc-1", "tc-2"}, func(abort func()) (agent.BeforeToolCallFunc, bool) {
			return func(_ context.Context, call agent.BeforeToolCallContext) *agent.BeforeToolCallResult {
				if call.ToolCall.ID == "tc-2" {
					abort()
				}
				return nil
			}, false
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newEndedCallRig(t)
			defer rig.playIncomingLoop()()
			run := &gateRun{}
			before, afterAssistant := tc.build(func() { run.agent.Abort() })
			startGateRun(t, run, tc.mode, tc.ids, gateTool{execute: rig.execute}, before, afterAssistant)
			run.wait(t)
			for _, id := range tc.ids {
				if text, isError := run.end(t, id); text != "Operation aborted" || !isError {
					t.Fatalf("call %s = %q error=%v, want \"Operation aborted\" as an error", id, text, isError)
				}
			}
			rig.expectSilence(t, 300*time.Millisecond)
		})
	}
}

// (b) The run is aborted after the loop's last gate, before the host writes the request (the tool's own entry is that point): the extension's execute is
// called, its signal is aborted, and the call's result is what it returned.
func TestAbortAfterTheLoopsLastGateEntersTheExtensionTool(t *testing.T) {
	for _, mode := range []agent.ToolExecutionMode{agent.ToolModeSequential, agent.ToolModeParallel} {
		t.Run(string(mode), func(t *testing.T) {
			rig := newEndedCallRig(t)
			defer rig.playIncomingLoop()()
			run := &gateRun{}
			var once sync.Once
			tool := gateTool{execute: rig.execute, enter: func() { once.Do(func() { run.agent.Abort() }) }}
			startGateRun(t, run, mode, []string{"tc-1"}, tool, nil, false)
			request := rig.enteredFrames(t)
			rig.clock.expectTimer(t, ToolAbortGrace)
			rig.respond(t, request, "answered after the abort")
			run.wait(t)
			if text, isError := run.end(t, "tc-1"); text != "answered after the abort" || isError {
				t.Fatalf("the call = %q error=%v, want what the extension returned", text, isError)
			}
		})
	}
}
