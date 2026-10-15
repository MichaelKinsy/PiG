package coding

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type runtimeAbortTool struct{ started chan struct{} }

func (runtimeAbortTool) Name() string  { return "block" }
func (runtimeAbortTool) Label() string { return "Block" }
func (runtimeAbortTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "block", Description: "Blocks until aborted", Parameters: map[string]any{"type": "object"}}
}
func (runtimeAbortTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeSequential }
func (tool runtimeAbortTool) Execute(ctx context.Context, _ string, _ json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	close(tool.started)
	<-ctx.Done()
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "tool aborted"}}, Details: map[string]any{}}, nil
}

// packages/coding-agent/test/suite/agent-session-runtime.test.ts:166.
func TestRuntimeOriginalSettlesActiveResponse(t *testing.T) {
	recordRuntimeOriginal(t, 166)
	started := make(chan struct{})
	var h *runtimeTestHarness
	switching, settledBeforeShutdown := false, false
	h = newRuntimeTestHarness(t, runtimeTestOptions{tools: []agent.AgentTool{runtimeAbortTool{started: started}}, extension: func() extension.Extension {
		return extension.Extension{Handlers: map[string][]extension.HandlerFn{"session_shutdown": {func(...any) (any, error) {
			if switching {
				var roles []string
				for _, entry := range h.runtime.Session().Entries() {
					if message, ok := entry.(icodingagent.MessageEntry); ok {
						roles = append(roles, message.Message.Role())
					}
				}
				settledBeforeShutdown = reflect.DeepEqual(roles, []string{"system", "user", "assistant", "toolResult", "assistant"})
			}
			return nil, nil
		}}}}
	}})
	runtimePrompt(t, h.runtime, "hello")
	first := h.runtime.Session().Path()
	if result, err := h.runtime.NewSession(t.Context(), nil); err != nil || result.Cancelled {
		t.Fatalf("new=%v error=%v", result, err)
	}
	if err := h.runtime.Session().BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	h.provider.mu.Lock()
	h.provider.responses = []scriptedResponse{fauxToolCall("block")}
	h.provider.requests = nil
	h.provider.mu.Unlock()
	outgoing := h.runtime.Session()
	prompt := make(chan error, 1)
	go func() { err := outgoing.Prompt(t.Context(), "start blocking tool"); prompt <- err }()
	joined := false
	t.Cleanup(func() {
		outgoing.RequestAbort()
		if !joined {
			<-prompt
		}
	})
	select {
	case <-started:
	case err := <-prompt:
		joined = true
		t.Fatalf("prompt settled without entering the blocking tool: %v", err)
	}
	switching = true
	result, err := h.runtime.SwitchSession(t.Context(), first)
	if err != nil || result.Cancelled {
		t.Fatalf("switch=%v error=%v", result, err)
	}
	if !settledBeforeShutdown {
		t.Fatal("session_shutdown preceded outgoing tool and response persistence")
	}
	promptErr := <-prompt
	joined = true
	if promptErr != nil {
		t.Fatalf("outgoing prompt rejected after replacement: %v", promptErr)
	}
	if h.runtime.Session() == outgoing || h.runtime.Session().Path() != first {
		t.Fatal("switch did not install destination Session")
	}
	// Replacement must have joined the outgoing tool and persisted its terminal response before returning.
	loaded, err := newSessionManagerForDir(h.runtime.Services(), outgoing.inner.GetSessionDir()).Load(outgoing.Path())
	if err != nil {
		t.Fatal(err)
	}
	var roles []string
	for _, entry := range loaded.GetEntries() {
		if message, ok := entry.(icodingagent.MessageEntry); ok {
			roles = append(roles, message.Message.Role())
		}
	}
	want := []string{"system", "user", "assistant", "toolResult", "assistant"}
	if !reflect.DeepEqual(roles, want) {
		t.Fatalf("outgoing persisted roles=%v want=%v", roles, want)
	}
}
