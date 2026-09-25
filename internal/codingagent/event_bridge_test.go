// event_bridge_test.go: tests for the extension event dispatch bridge.
//
// tearout: legacy ExtensionRunner tests removed. Remaining tests
// verify the inproc.Runner dispatch path.

package codingagent

import (
	"slices"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// makeFreshWithHandler registers a single handler for `eventType` via
// a fixture extension.
func makeFreshWithHandler(t *testing.T, eventType string, hook func()) *inproc.Runner {
	t.Helper()
	ext := extension.Extension{
		Path:         "/fixture/event_bridge",
		ResolvedPath: "/fixture/event_bridge",
		Handlers: map[string][]extension.HandlerFn{
			eventType: {
				func(_ ...any) (any, error) {
					hook()
					return nil, nil
				},
			},
		},
	}
	return inproc.NewRunner([]extension.Extension{ext}, ".")
}

func TestEmitSessionStart_BridgesToRunner(t *testing.T) {
	var fired atomic.Int32
	fresh := makeFreshWithHandler(t, EventSessionStart, func() { fired.Add(1) })
	emitSessionStart(fresh, "startup")
	if got := fired.Load(); got != 1 {
		t.Errorf("session_start handler fired %d times, want 1", got)
	}
}

func TestEmitSessionInfoChanged_BridgesPayload(t *testing.T) {
	var capturedName atomic.Value
	ext := extension.Extension{
		Path:         "/fixture/session-info",
		ResolvedPath: "/fixture/session-info",
		Handlers: map[string][]extension.HandlerFn{
			EventSessionInfoChanged: {
				func(args ...any) (any, error) {
					if len(args) >= 1 {
						if e, ok := args[0].(extension.SessionInfoChangedEvent); ok {
							capturedName.Store(e.Name)
						}
					}
					return nil, nil
				},
			},
		},
	}
	fresh := inproc.NewRunner([]extension.Extension{ext}, ".")
	emitSessionInfoChanged(fresh, "work")

	got := capturedName.Load()
	if got == nil {
		t.Fatal("handler did not receive typed SessionInfoChangedEvent")
	}
	if got.(string) != "work" {
		t.Errorf("captured name = %q, want work", got)
	}
}

func TestEmitSessionShutdown_BridgesToRunner(t *testing.T) {
	for _, reason := range []string{"quit", "slash-quit"} {
		t.Run(reason, func(t *testing.T) {
			var fired atomic.Int32
			fresh := makeFreshWithHandler(t, EventSessionShutdown, func() { fired.Add(1) })
			emitSessionShutdown(fresh, reason)
			if got := fired.Load(); got != 1 {
				t.Errorf("session_shutdown handler fired %d times, want 1", got)
			}
		})
	}
}

func TestEmitSessionShutdown_SlashQuitCollapsedToQuit(t *testing.T) {
	var capturedReason atomic.Value
	ext := extension.Extension{
		Path:         "/fixture/slash-quit",
		ResolvedPath: "/fixture/slash-quit",
		Handlers: map[string][]extension.HandlerFn{
			EventSessionShutdown: {
				func(args ...any) (any, error) {
					if len(args) >= 1 {
						if e, ok := args[0].(extension.SessionShutdownEvent); ok {
							capturedReason.Store(e.Reason)
						}
					}
					return nil, nil
				},
			},
		},
	}
	fresh := inproc.NewRunner([]extension.Extension{ext}, ".")
	emitSessionShutdown(fresh, "slash-quit")

	got := capturedReason.Load()
	if got == nil {
		t.Fatal("handler did not capture reason")
	}
	if got.(string) != "quit" {
		t.Errorf("reason = %q, want %q (slash-quit must collapse to quit)", got, "quit")
	}
}

func TestEmitHelpers_HasHandlersShortCircuit(t *testing.T) {
	ext := extension.Extension{
		Path:         "/fixture/different-event",
		ResolvedPath: "/fixture/different-event",
		Handlers: map[string][]extension.HandlerFn{
			EventUserBash: {
				func(_ ...any) (any, error) { return nil, nil },
			},
		},
	}
	fresh := inproc.NewRunner([]extension.Extension{ext}, ".")
	fresh.Invalidate("test")

	// These should short-circuit on HasHandlers, not reaching stale Emit.
	emitSessionStart(fresh, "startup")
	emitBeforeAgentStart(fresh, "x", "y", extension.BuildSystemPromptOptions{})
	emitSessionShutdown(fresh, "quit")
	_, _ = emitUserBash(fresh, "ls", "/", false)
}

func TestEmitAgentSettled_BridgesToRunner(t *testing.T) {
	var fired atomic.Int32
	fresh := makeFreshWithHandler(t, EventAgentSettled, func() { fired.Add(1) })
	emitAgentSettled(fresh)
	if got := fired.Load(); got != 1 {
		t.Errorf("agent_settled handler fired %d times, want 1", got)
	}
}

func TestEmitAgentSettled_ShortCircuitsWithoutHandler(t *testing.T) {
	// A runner whose only handler is for a different event must not dispatch
	// agent_settled (negative control: proves the HasHandlers guard gates it).
	var fired atomic.Int32
	fresh := makeFreshWithHandler(t, EventUserBash, func() { fired.Add(1) })
	emitAgentSettled(fresh)
	emitAgentSettled(nil) // nil runner must be a no-op, not a panic
	if got := fired.Load(); got != 0 {
		t.Errorf("agent_settled fired %d times without a registered handler, want 0", got)
	}
}

func TestEmitBeforeAgentStart_BridgesToRunner(t *testing.T) {
	var fired atomic.Int32
	fresh := makeFreshWithHandler(t, EventBeforeAgentStart, func() { fired.Add(1) })
	_ = emitBeforeAgentStart(fresh, "hello", "you are helpful", extension.BuildSystemPromptOptions{})
	if got := fired.Load(); got != 1 {
		t.Errorf("before_agent_start handler fired %d times, want 1", got)
	}
}

func TestEmitBeforeAgentStart_ForwardsSystemPromptOptionsToHandler(t *testing.T) {
	// Lock the contract: the structured BuildSystemPromptOptions handed
	// to emitBeforeAgentStart must reach extension handlers verbatim on
	// event.systemPromptOptions. This mirrors upstream agent-session.ts
	// _rebuildSystemPrompt + before_agent_start dispatch.
	var captured atomic.Value
	ext := extension.Extension{
		Path:         "/fixture/sp-options",
		ResolvedPath: "/fixture/sp-options",
		Handlers: map[string][]extension.HandlerFn{
			EventBeforeAgentStart: {
				func(args ...any) (any, error) {
					if len(args) >= 1 {
						if e, ok := args[0].(extension.BeforeAgentStartEvent); ok {
							captured.Store(e.SystemPromptOptions)
						}
					}
					return nil, nil
				},
			},
		},
	}
	fresh := inproc.NewRunner([]extension.Extension{ext}, "/tmp/cwd")
	opts := extension.BuildSystemPromptOptions{
		CustomPrompt:       "custom",
		SelectedTools:      []string{"read", "edit"},
		ToolSnippets:       map[string]string{"read": "Read files"},
		PromptGuidelines:   []string{"be concise"},
		AppendSystemPrompt: "appended text",
		Cwd:                "/tmp/cwd",
		ContextFiles: []extension.SystemPromptContextFile{
			{Path: "/x/AGENTS.md", Content: "agents body"},
		},
		Skills: []extension.SystemPromptSkill{
			{Name: "commit", Description: "make a commit"},
		},
	}
	_ = emitBeforeAgentStart(fresh, "hello", "original-sp", opts)

	raw := captured.Load()
	if raw == nil {
		t.Fatal("handler did not capture SystemPromptOptions")
	}
	got, ok := raw.(extension.BuildSystemPromptOptions)
	if !ok {
		t.Fatalf("captured type %T, want extension.BuildSystemPromptOptions", raw)
	}
	if got.CustomPrompt != "custom" {
		t.Errorf("CustomPrompt = %q", got.CustomPrompt)
	}
	if got.AppendSystemPrompt != "appended text" {
		t.Errorf("AppendSystemPrompt = %q", got.AppendSystemPrompt)
	}
	if got.Cwd != "/tmp/cwd" {
		t.Errorf("Cwd = %q", got.Cwd)
	}
	if len(got.SelectedTools) != 2 || got.SelectedTools[0] != "read" {
		t.Errorf("SelectedTools = %v", got.SelectedTools)
	}
	if len(got.ContextFiles) != 1 || got.ContextFiles[0].Path != "/x/AGENTS.md" {
		t.Errorf("ContextFiles = %+v", got.ContextFiles)
	}
	if len(got.Skills) != 1 || got.Skills[0].Name != "commit" {
		t.Errorf("Skills = %+v", got.Skills)
	}
}

func TestEmitBeforeAgentStart_ReturnsMutatedSystemPrompt(t *testing.T) {
	ext := extension.Extension{
		Path:         "/fixture/before-agent-start-result",
		ResolvedPath: "/fixture/before-agent-start-result",
		Handlers: map[string][]extension.HandlerFn{
			EventBeforeAgentStart: {
				func(args ...any) (any, error) {
					return &extension.BeforeAgentStartEventResult{SystemPrompt: new("rewritten-sp")}, nil
				},
			},
		},
	}
	fresh := inproc.NewRunner([]extension.Extension{ext}, ".")
	got := emitBeforeAgentStart(fresh, "hello", "original-sp", extension.BuildSystemPromptOptions{})
	if got == nil {
		t.Fatal("got = nil, want combined result")
		return
	}
	if got.SystemPrompt == nil || *got.SystemPrompt != "rewritten-sp" {
		t.Errorf("SystemPrompt = %v, want rewritten-sp", got.SystemPrompt)
	}
}

func TestEmitThinkingLevelSelect_BridgesToRunner(t *testing.T) {
	var gotLevel atomic.Value
	var gotPrevious atomic.Value
	ext := extension.Extension{
		Path:         "/fixture/thinking",
		ResolvedPath: "/fixture/thinking",
		Handlers: map[string][]extension.HandlerFn{
			EventThinkingLevelSelect: {
				func(args ...any) (any, error) {
					if len(args) >= 1 {
						if e, ok := args[0].(extension.ThinkingLevelSelectEvent); ok {
							gotLevel.Store(e.Level)
							gotPrevious.Store(e.PreviousLevel)
						}
					}
					return nil, nil
				},
			},
		},
	}
	fresh := inproc.NewRunner([]extension.Extension{ext}, ".")
	emitThinkingLevelSelect(fresh, "high", "medium")
	if gotLevel.Load() != "high" || gotPrevious.Load() != "medium" {
		t.Fatalf("thinking event = %v/%v, want high/medium", gotLevel.Load(), gotPrevious.Load())
	}
}

func TestEmitUserBash_BridgesToRunner(t *testing.T) {
	var fired atomic.Int32
	fresh := makeFreshWithHandler(t, EventUserBash, func() { fired.Add(1) })
	if _, err := emitUserBash(fresh, "ls -la", "/tmp", false); err != nil {
		t.Fatal(err)
	}
	if got := fired.Load(); got != 1 {
		t.Errorf("user_bash handler fired %d times, want 1", got)
	}
}

func TestEmitHelpers_NilRunnerIsNoOp(t *testing.T) {
	emitSessionStart(nil, "startup")
	emitSessionInfoChanged(nil, "work")
	emitSessionShutdown(nil, "quit")
	_ = emitBeforeAgentStart(nil, "x", "y", extension.BuildSystemPromptOptions{})
	_, _ = emitUserBash(nil, "ls", "/", false)
	emitAgentStart(nil)
	emitAgentEnd(nil, nil, false)
	emitTurnStart(nil, 0)
	emitTurnEnd(nil, agent.TurnEndEvent{})
	emitMessageStart(nil, agent.AgentMessage{})
	emitMessageEnd(nil, agent.AgentMessage{})
	emitToolExecutionStart(nil, "id", "bash", nil)
	emitToolExecutionUpdate(nil, "id", "bash", "partial", nil, nil)
	emitToolExecutionEnd(nil, "id", "bash", agent.AgentToolResult{Content: "ok"})
	emitMessageUpdate(nil, nil, nil)
	emitThinkingLevelSelect(nil, "high", "medium")
}

func TestEmitSessionStart_BridgesPayloadFields(t *testing.T) {
	var capturedReason atomic.Value
	ext := extension.Extension{
		Path:         "/fixture/payload",
		ResolvedPath: "/fixture/payload",
		Handlers: map[string][]extension.HandlerFn{
			EventSessionStart: {
				func(args ...any) (any, error) {
					if len(args) >= 1 {
						if e, ok := args[0].(extension.SessionStartEvent); ok {
							capturedReason.Store(e.Reason)
						}
					}
					return nil, nil
				},
			},
		},
	}
	fresh := inproc.NewRunner([]extension.Extension{ext}, ".")
	emitSessionStart(fresh, "fork")

	got := capturedReason.Load()
	if got == nil {
		t.Fatal("handler did not receive typed SessionStartEvent")
	}
	if got.(string) != "fork" {
		t.Errorf("captured reason = %q, want fork", got)
	}
}

func TestEmitAgentStart_BridgesToRunner(t *testing.T) {
	var fired atomic.Int32
	fresh := makeFreshWithHandler(t, EventAgentStart, func() { fired.Add(1) })
	emitAgentStart(fresh)
	if got := fired.Load(); got != 1 {
		t.Errorf("agent_start handler fired %d times, want 1", got)
	}
}

func TestEmitAgentEnd_BridgesToRunner(t *testing.T) {
	var fired atomic.Int32
	fresh := makeFreshWithHandler(t, EventAgentEnd, func() { fired.Add(1) })
	emitAgentEnd(fresh, nil, false)
	if got := fired.Load(); got != 1 {
		t.Errorf("agent_end handler fired %d times, want 1", got)
	}
}

func TestEmitAgentEnd_BridgesPayload(t *testing.T) {
	var captured atomic.Value
	ext := extension.Extension{
		Path:         "/fixture/agentend",
		ResolvedPath: "/fixture/agentend",
		Handlers: map[string][]extension.HandlerFn{
			EventAgentEnd: {
				func(args ...any) (any, error) {
					if len(args) >= 1 {
						captured.Store(args[0])
					}
					return nil, nil
				},
			},
		},
	}
	fresh := inproc.NewRunner([]extension.Extension{ext}, ".")
	msgs := []agent.AgentMessage{{}, {}}
	emitAgentEnd(fresh, msgs, true)
	got := captured.Load()
	if got == nil {
		t.Fatal("handler did not fire")
	}
	e := got.(extension.AgentEndEvent)
	if len(e.Messages) != 2 {
		t.Errorf("messages len = %d, want 2", len(e.Messages))
	}
}

func TestEmitTurnStart_BridgesToRunner(t *testing.T) {
	var fired atomic.Int32
	fresh := makeFreshWithHandler(t, EventTurnStart, func() { fired.Add(1) })
	emitTurnStart(fresh, 0)
	if got := fired.Load(); got != 1 {
		t.Errorf("turn_start handler fired %d times, want 1", got)
	}
}

func TestEmitTurnEnd_BridgesToRunner(t *testing.T) {
	var fired atomic.Int32
	fresh := makeFreshWithHandler(t, EventTurnEnd, func() { fired.Add(1) })
	emitTurnEnd(fresh, agent.TurnEndEvent{})
	if got := fired.Load(); got != 1 {
		t.Errorf("turn_end handler fired %d times, want 1", got)
	}
}

func TestEmitTurnEnd_BridgesPayload(t *testing.T) {
	var captured atomic.Value
	ext := extension.Extension{
		Path:         "/fixture/turnend",
		ResolvedPath: "/fixture/turnend",
		Handlers: map[string][]extension.HandlerFn{
			EventTurnEnd: {
				func(args ...any) (any, error) {
					if len(args) >= 1 {
						captured.Store(args[0])
					}
					return nil, nil
				},
			},
		},
	}
	fresh := inproc.NewRunner([]extension.Extension{ext}, ".")
	trs := []agent.ToolResultMessage{{Role: agent.RoleToolResult}}
	emitTurnEnd(fresh, agent.TurnEndEvent{
		TurnIndex:          5,
		ToolResults:        trs,
		MessageEntryID:     "assistant-entry",
		ToolResultEntryIDs: []string{"tool-entry"},
	})
	got := captured.Load()
	if got == nil {
		t.Fatal("handler did not fire")
	}
	e := got.(extension.TurnEndEvent)
	if e.TurnIndex != 5 {
		t.Errorf("turnIndex = %d, want 5", e.TurnIndex)
	}
	if len(e.ToolResults) != 1 {
		t.Errorf("toolResults len = %d, want 1", len(e.ToolResults))
	}
	if e.MessageEntryID != "assistant-entry" || !slices.Equal(e.ToolResultEntryIds, []string{"tool-entry"}) {
		t.Errorf("entry IDs = %q/%q", e.MessageEntryID, e.ToolResultEntryIds)
	}
}

func TestEmitMessageStart_BridgesToRunner(t *testing.T) {
	var fired atomic.Int32
	fresh := makeFreshWithHandler(t, EventMessageStart, func() { fired.Add(1) })
	emitMessageStart(fresh, agent.AgentMessage{})
	if got := fired.Load(); got != 1 {
		t.Errorf("message_start handler fired %d times, want 1", got)
	}
}

func TestEmitMessageEnd_BridgesToRunner(t *testing.T) {
	var fired atomic.Int32
	fresh := makeFreshWithHandler(t, EventMessageEnd, func() { fired.Add(1) })
	emitMessageEnd(fresh, agent.AgentMessage{})
	if got := fired.Load(); got != 1 {
		t.Errorf("message_end handler fired %d times, want 1", got)
	}
}

func TestEmitToolExecutionStart_BridgesToRunner(t *testing.T) {
	var fired atomic.Int32
	fresh := makeFreshWithHandler(t, EventToolExecutionStart, func() { fired.Add(1) })
	emitToolExecutionStart(fresh, "tc-1", "bash", []byte(`{"command":"ls"}`))
	if got := fired.Load(); got != 1 {
		t.Errorf("tool_execution_start handler fired %d times, want 1", got)
	}
}

func TestEmitToolExecutionEnd_BridgesToRunner(t *testing.T) {
	var fired atomic.Int32
	fresh := makeFreshWithHandler(t, EventToolExecutionEnd, func() { fired.Add(1) })
	emitToolExecutionEnd(fresh, "tc-1", "bash", agent.AgentToolResult{Content: "output text"})
	if got := fired.Load(); got != 1 {
		t.Errorf("tool_execution_end handler fired %d times, want 1", got)
	}
}

func TestEmitTurnStart_PayloadFields(t *testing.T) {
	var capturedIdx atomic.Int32
	capturedIdx.Store(-1)
	ext := extension.Extension{
		Path:         "/fixture/turn",
		ResolvedPath: "/fixture/turn",
		Handlers: map[string][]extension.HandlerFn{
			EventTurnStart: {
				func(args ...any) (any, error) {
					if len(args) >= 1 {
						if e, ok := args[0].(extension.TurnStartEvent); ok {
							capturedIdx.Store(int32(e.TurnIndex))
						}
					}
					return nil, nil
				},
			},
		},
	}
	fresh := inproc.NewRunner([]extension.Extension{ext}, ".")
	emitTurnStart(fresh, 3)
	if got := capturedIdx.Load(); got != 3 {
		t.Errorf("turnIndex = %d, want 3", got)
	}
}

func TestEmitToolExecutionEnd_PayloadFields(t *testing.T) {
	var capturedName atomic.Value
	var capturedError atomic.Value
	ext := extension.Extension{
		Path:         "/fixture/tool-end",
		ResolvedPath: "/fixture/tool-end",
		Handlers: map[string][]extension.HandlerFn{
			EventToolExecutionEnd: {
				func(args ...any) (any, error) {
					if len(args) >= 1 {
						if e, ok := args[0].(extension.ToolExecutionEndEvent); ok {
							capturedName.Store(e.ToolName)
							capturedError.Store(e.IsError)
						}
					}
					return nil, nil
				},
			},
		},
	}
	fresh := inproc.NewRunner([]extension.Extension{ext}, ".")
	emitToolExecutionEnd(fresh, "tc-2", "read", agent.AgentToolResult{Content: "file contents", IsError: true})
	if got := capturedName.Load().(string); got != "read" {
		t.Errorf("toolName = %q, want read", got)
	}
	if got := capturedError.Load().(bool); !got {
		t.Error("isError = false, want true")
	}
}

func completedTestStream(provider string) *ai.AssistantMessageEventStream {
	partial := &ai.AssistantMessage{Provider: provider, Model: "test", StopReason: ai.StopReasonPending}
	final := &ai.AssistantMessage{Provider: provider, Model: "test", StopReason: ai.StopReasonStop}
	stream := ai.NewAssistantMessageEventStream()
	if err := stream.Push(ai.StartEvent{Partial: partial}); err != nil {
		panic(err)
	}
	if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: final}); err != nil {
		panic(err)
	}
	return stream
}
