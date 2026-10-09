package coding

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

type upstreamAbortTool struct{ started chan struct{} }

func (*upstreamAbortTool) Name() string                           { return "block" }
func (*upstreamAbortTool) Label() string                          { return "Block" }
func (*upstreamAbortTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (*upstreamAbortTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "block", Description: "Wait until aborted", Parameters: map[string]any{"type": "object"}}
}
func (tool *upstreamAbortTool) Execute(ctx context.Context, _ string, _ json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	close(tool.started)
	<-ctx.Done()
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "tool aborted"}}, Details: map[string]any{}}, nil
}

// Ports packages/coding-agent/test/suite/regressions/8724-in-memory-fork-active-tool.test.ts:22: createAgentSessionRuntime runs the factory for the first Session.
func TestInMemoryForkDoesNotAppendAbortedTurnToReplacementUpstream(t *testing.T) {
	testInMemoryForkDoesNotAppendAbortedTurn(t, false)
}

// The same regression with the test's own construction (8724-in-memory-fork-active-tool.test.ts:62): `new AgentSessionRuntime(harness.session, services, createRuntime)`
// wraps a Session that already exists, and only the replacement comes from the factory.
func TestInMemoryForkDoesNotAppendAbortedTurnWhenTheRuntimeWrapsAnExistingSessionUpstream(t *testing.T) {
	testInMemoryForkDoesNotAppendAbortedTurn(t, true)
}

func testInMemoryForkDoesNotAppendAbortedTurn(t *testing.T, wrapExisting bool) {
	t.Helper()
	services := newTestServices(t)
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("first response")}, StopReason: "stop"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("block", map[string]any{}, &ai.FauxToolCallOptions{ID: "block-call"})}, StopReason: "toolUse"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("unused after abort")}, StopReason: "stop"}),
	})
	tool := &upstreamAbortTool{started: make(chan struct{})}
	model := &ai.Model{ID: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 128000}}
	manager, err := NewInMemorySessionManager(services.CWD())
	if err != nil {
		t.Fatal(err)
	}
	first := true
	factory := func(_ context.Context, options CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		optionsForSession := SessionOptions{SessionManager: options.SessionManager, Model: model, SkipBuiltinTools: true}
		if first {
			optionsForSession.Tools = []agent.AgentTool{tool}
			first = false
		}
		session, err := NewSession(services, optionsForSession)
		if err == nil {
			drainSessionEvents(t, session)
		}
		return CreateAgentSessionRuntimeResult{Session: session, Services: services}, err
	}
	var runtime *Runtime
	if wrapExisting {
		harnessSession, err := NewSession(services, SessionOptions{SessionManager: manager, Model: model, SkipBuiltinTools: true, Tools: []agent.AgentTool{tool}})
		if err != nil {
			t.Fatal(err)
		}
		drainSessionEvents(t, harnessSession)
		first = false
		runtime, err = NewAgentSessionRuntime(t.Context(), harnessSession, services, factory, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if runtime.Session() != harnessSession || runtime.Services() != services || runtime.CWD() != services.CWD() {
			t.Fatal("the runtime does not own the Session and Services it was constructed with")
		}
	} else {
		var err error
		runtime, err = CreateAgentSessionRuntime(t.Context(), factory, CreateAgentSessionRuntimeOptions{CWD: services.CWD(), AgentDir: services.AgentDir(), SessionManager: manager})
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := runtime.Session().Send(t.Context(), "first prompt"); err != nil {
		t.Fatal(err)
	}
	messages := runtime.Session().UserMessagesForForking()
	if len(messages) == 0 {
		t.Fatal("no first user entry")
	}
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	outgoing := make(chan error, 1)
	source := runtime.Session()
	go func() { _, err := source.Send(ctx, "start blocking tool"); outgoing <- err }()
	select {
	case <-tool.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	fork, err := runtime.Fork(ctx, messages[0].EntryID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fork.Cancelled || fork.SelectedText == nil || *fork.SelectedText != "first prompt" {
		t.Fatalf("fork=%+v", fork)
	}
	if err := <-outgoing; err != nil {
		t.Fatalf("outgoing prompt rejected: %v", err)
	}
	if err := runtime.Session().BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	var roles, entryRoles []string
	for _, message := range runtime.Session().Messages() {
		roles = append(roles, message.Role())
	}
	for _, entry := range runtime.Session().Inner().GetEntries() {
		if message, ok := entry.(icodingagent.MessageEntry); ok {
			entryRoles = append(entryRoles, message.Message.Role())
		}
	}
	if !reflect.DeepEqual(roles, []string{"system"}) || !reflect.DeepEqual(entryRoles, []string{"system"}) {
		t.Fatalf("replacement roles=%q entry roles=%q", roles, entryRoles)
	}
	var capturedRoles []string
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		for _, message := range request.Messages() {
			switch message.(type) {
			case ai.SystemMessage:
				capturedRoles = append(capturedRoles, "system")
			case ai.UserMessage:
				capturedRoles = append(capturedRoles, "user")
			case ai.AssistantMessage:
				capturedRoles = append(capturedRoles, "assistant")
			case ai.ToolResultMessage:
				capturedRoles = append(capturedRoles, "toolResult")
			default:
				capturedRoles = append(capturedRoles, "unknown")
			}
		}
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("next response")}, StopReason: "stop"}.AssistantMessage(), nil
	})})
	if _, err := runtime.Session().Send(t.Context(), "next prompt"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(capturedRoles, []string{"system", "system", "user"}) {
		t.Fatalf("next request roles=%q", capturedRoles)
	}
}

// agent-session-runtime.ts:81-93 and the diagnostics / modelFallbackMessage getters: the constructor's fifth and sixth inputs are what the runtime reports until a
// replacement applies its own; the Session and Services are the ones given.
func TestNewAgentSessionRuntimeReportsItsConstructionInputs(t *testing.T) {
	services := newTestServices(t)
	manager, err := NewInMemorySessionManager(services.CWD())
	if err != nil {
		t.Fatal(err)
	}
	model := &ai.Model{ID: "faux-1", Provider: ai.NewFauxProvider(ai.FauxConfig{}), Capabilities: ai.ModelCapabilities{ContextWindow: 128000}}
	session, err := NewSession(services, SessionOptions{SessionManager: manager, Model: model, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, session)
	diagnostics := []AgentSessionRuntimeDiagnostic{{Type: "warning", Message: "startup diagnostic"}}
	factory := func(context.Context, CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		return CreateAgentSessionRuntimeResult{}, errors.New("unused")
	}
	runtime, err := NewAgentSessionRuntime(t.Context(), session, services, factory, diagnostics, "no model")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if !reflect.DeepEqual(runtime.Diagnostics(), diagnostics) || runtime.ModelFallbackMessage() != "no model" {
		t.Fatalf("diagnostics %+v, model fallback %q", runtime.Diagnostics(), runtime.ModelFallbackMessage())
	}
	if _, err := NewAgentSessionRuntime(t.Context(), nil, services, factory, nil, ""); err == nil {
		t.Fatal("a runtime without a Session was constructed")
	}
}

// agent-session.ts:509 keeps config.sessionStartEvent on the AgentSession, and neither `new AgentSessionRuntime(...)` (agent-session-runtime.ts:81-93) nor
// createAgentSessionRuntime (:426-438, which hands options.sessionStartEvent to the factory) replaces it: the session_start the Session's extensions
// receive is the one it was created with, not a fresh "startup".
// mutation-checked: NewAgentSessionRuntime passing a pointer to the Session's own start event, which applyRuntime resets to "startup" before reading it, fails both cases.
func TestAgentSessionRuntimeKeepsTheSessionStartEventUpstream(t *testing.T) {
	resume := extension.SessionStartEvent{Type: "session_start", Reason: "resume", PreviousSessionFile: "/sessions/previous.jsonl"}
	t.Run("new AgentSessionRuntime", func(t *testing.T) {
		services := newTestServices(t)
		manager, err := NewInMemorySessionManager(services.CWD())
		if err != nil {
			t.Fatal(err)
		}
		session, err := NewSession(services, SessionOptions{SessionManager: manager, SkipBuiltinTools: true})
		if err != nil {
			t.Fatal(err)
		}
		drainSessionEvents(t, session)
		session.sessionStartEvent = resume
		factory := func(context.Context, CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
			return CreateAgentSessionRuntimeResult{}, errors.New("unused")
		}
		runtime, err := NewAgentSessionRuntime(t.Context(), session, services, factory, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		if got := runtime.Session().StartEvent(); got != resume {
			t.Fatalf("session_start = %+v, want the Session's own %+v", got, resume)
		}
	})
	t.Run("createAgentSessionRuntime", func(t *testing.T) {
		services := newTestServices(t)
		manager, err := NewInMemorySessionManager(services.CWD())
		if err != nil {
			t.Fatal(err)
		}
		factory := func(_ context.Context, options CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
			session, err := NewSession(services, SessionOptions{SessionManager: options.SessionManager, SkipBuiltinTools: true})
			if err == nil {
				drainSessionEvents(t, session)
			}
			return CreateAgentSessionRuntimeResult{Session: session, Services: services}, err
		}
		runtime, err := CreateAgentSessionRuntime(t.Context(), factory, CreateAgentSessionRuntimeOptions{CWD: services.CWD(), AgentDir: services.AgentDir(), SessionManager: manager, SessionStartEvent: &resume})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		if got := runtime.Session().StartEvent(); got != resume {
			t.Fatalf("session_start = %+v, want options.sessionStartEvent %+v", got, resume)
		}
	})
}
