package coding

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

func bashHasPending(session *Session) bool {
	session.pendingBashMu.Lock()
	defer session.pendingBashMu.Unlock()
	return len(session.pendingBashMessages) > 0
}

func bashIsRunning(session *Session) bool {
	session.bashMu.Lock()
	defer session.bashMu.Unlock()
	return len(session.bashCancels) > 0
}

type bashPersistenceWaitTool struct{ release <-chan struct{} }

func (bashPersistenceWaitTool) Name() string                           { return "wait" }
func (bashPersistenceWaitTool) Label() string                          { return "Wait" }
func (bashPersistenceWaitTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (bashPersistenceWaitTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "wait", Description: "Wait for release", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (tool bashPersistenceWaitTool) Execute(_ context.Context, _ string, _ json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	<-tool.release
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "released"}}, Details: map[string]any{}}, nil
}

type bashPersistenceEchoTool struct{}

func (bashPersistenceEchoTool) Name() string  { return "echo" }
func (bashPersistenceEchoTool) Label() string { return "Echo" }
func (bashPersistenceEchoTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "echo", Description: "Echo text back", Parameters: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}}}
}
func (bashPersistenceEchoTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }

func bashPersistenceEchoCalls(texts ...string) scriptedResponse {
	return func([]ai.Message) *ai.AssistantMessage {
		content := make([]ai.AssistantContentBlock, len(texts))
		for i, text := range texts {
			content[i] = ai.ToolCall{ID: "call-" + text, Name: "echo", Arguments: ai.JsonObject{"text": text}}
		}
		return &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse, Content: content}
	}
}

func (bashPersistenceEchoTool) Execute(_ context.Context, _ string, args json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var params struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.AgentToolResult{}, err
	}
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "echo:" + params.Text}}, Details: map[string]any{"text": params.Text}}, nil
}

// bashPersistenceAbortProvider retains the original 20,000-character response and suspends completion after its first streamed chunk until Abort reaches the producer.
type bashPersistenceAbortProvider struct {
	text string
	done chan struct{}
}

func (*bashPersistenceAbortProvider) ID() string   { return "faux" }
func (*bashPersistenceAbortProvider) Close() error { return nil }
func (provider *bashPersistenceAbortProvider) Stream(ctx context.Context, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	chunk := provider.text[:min(len(provider.text), 16)]
	partial := &ai.AssistantMessage{Provider: "faux", Model: "faux-1", Content: []ai.AssistantContentBlock{ai.TextContent{Text: chunk}}, StopReason: ai.StopReasonPending, Timestamp: time.Now().UnixMilli()}
	stream := newSessionTestStream(ai.StartEvent{Partial: partial}, ai.TextDeltaEvent{ContentIndex: 0, Delta: chunk, Partial: partial})
	go func() {
		defer close(provider.done)
		<-ctx.Done()
		terminal := *partial
		terminal.StopReason = ai.StopReasonAborted
		terminal.ErrorMessage = "aborted"
		_ = stream.Push(ai.ErrorEvent{Reason: ai.StopReasonAborted, Error: &terminal})
	}()
	return stream, nil
}

type controlledBashPersistenceInvocation struct {
	signal context.Context
	finish func()
}

func controlledBashPersistenceOperations() (<-chan controlledBashPersistenceInvocation, bashPersistenceOperations) {
	invocations := make(chan controlledBashPersistenceInvocation, 2)
	operations := bashPersistenceOperations(func(ctx context.Context, _, _ string, _ extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
		release := make(chan struct{})
		invocations <- controlledBashPersistenceInvocation{signal: ctx, finish: sync.OnceFunc(func() { close(release) })}
		<-release
		return extension.BashOperationsResult{ExitCode: new(0)}, nil
	})
	return invocations, operations
}

// startBashPersistenceCall owns and joins the awaited operation; callers observe the exec prologue through invocations before asserting its signal.
func startBashPersistenceCall(t *testing.T, session *Session, command string, operations extension.BashOperations) func() BashResult {
	t.Helper()
	var result BashResult
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		result, err = session.ExecuteBashWithOperations(t.Context(), command, false, nil, operations, nil)
	}()
	join := func() BashResult {
		<-done
		if err != nil {
			t.Error(err)
		}
		return result
	}
	t.Cleanup(func() { session.AbortBash(); join() })
	return join
}
