package coding

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
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

// Ports packages/coding-agent/test/suite/regressions/8724-in-memory-fork-active-tool.test.ts:22.
func TestInMemoryForkDoesNotAppendAbortedTurnToReplacementUpstream(t *testing.T) {
	services := newTestServices(t)
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("first response")}, StopReason: "stop"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("block", map[string]any{}, "block-call")}, StopReason: "toolUse"}),
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
	runtime, err := CreateAgentSessionRuntime(t.Context(), factory, CreateAgentSessionRuntimeOptions{CWD: services.CWD(), AgentDir: services.AgentDir(), SessionManager: manager})
	if err != nil {
		t.Fatal(err)
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
	if err := runtime.Session().BindExtensions(t.Context()); err != nil {
		t.Fatal(err)
	}
	var roles, entryRoles []string
	for _, message := range runtime.Session().Messages() {
		roles = append(roles, message.Role())
	}
	for _, entry := range runtime.Session().Inner().Entries() {
		if message, ok := entry.AsMessage(); ok {
			entryRoles = append(entryRoles, message.Message.Role())
		}
	}
	if !reflect.DeepEqual(roles, []string{"system"}) || !reflect.DeepEqual(entryRoles, []string{"system"}) {
		t.Fatalf("replacement roles=%q entry roles=%q", roles, entryRoles)
	}
	var capturedRoles []string
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.FauxResponse, error) {
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
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("next response")}, StopReason: "stop"}, nil
	})})
	if _, err := runtime.Session().Send(t.Context(), "next prompt"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(capturedRoles, []string{"system", "system", "user"}) {
		t.Fatalf("next request roles=%q", capturedRoles)
	}
}
