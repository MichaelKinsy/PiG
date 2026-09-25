package codingagent

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// alwaysToolCallProvider answers every request with one tool call, so the run
// only ends when the loop stops asking.
type alwaysToolCallProvider struct{}

func (alwaysToolCallProvider) ID() string   { return "always-tool" }
func (alwaysToolCallProvider) Close() error { return nil }
func (alwaysToolCallProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	call := ai.ToolCall{ID: "call", Name: "probe", Arguments: ai.JsonObject{}}
	partial := &ai.AssistantMessage{Provider: "always-tool", Model: "m", StopReason: ai.StopReasonPending}
	final := &ai.AssistantMessage{Provider: "always-tool", Model: "m", StopReason: ai.StopReasonToolUse, Content: []ai.AssistantContentBlock{call}}
	stream := ai.NewAssistantMessageEventStream()
	_ = stream.Push(ai.StartEvent{Partial: partial})
	_ = stream.Push(ai.DoneEvent{Reason: ai.StopReasonToolUse, Message: final})
	return stream, nil
}

type probeTool struct{}

func (probeTool) Name() string                           { return "probe" }
func (probeTool) Label() string                          { return "probe" }
func (probeTool) Description() string                    { return "probe" }
func (probeTool) Schema() ai.ToolSchema                  { return ai.ToolSchema{Name: "probe"} }
func (probeTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeSequential }
func (probeTool) Execute(context.Context, string, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return agent.AgentToolResult{Content: "LOCK ACQUIRED"}, nil
}

// A run that stops with a tool result unanswered must not leave the UI idle
// with no explanation: the integrator worker sat idle for minutes with its last
// message a tool result and nothing on screen. The error from the agent reaches
// the transcript through runTurn's error path.
func TestInteractiveRunEndingOnUnansweredToolResultShowsError(t *testing.T) {
	model := &ai.Model{ID: "m", DisplayName: "m", Provider: alwaysToolCallProvider{}, Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: model})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewStatusLine(model, "", nil)
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model, Tools: []agent.AgentTool{probeTool{}}, MaxTurns: 2})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go m.drainLoop(ctx, loopDone)

	m.handleSubmit(ctx, "go")

	deadline := time.Now().Add(10 * time.Second)
	for {
		done := make(chan string, 1)
		m.runOnMain(ctx, func() {
			lines := m.chatContainer.Render(100)
			for i, line := range lines {
				lines[i] = widthx.StripAnsi(line)
			}
			if !m.turnActive.Load() {
				done <- strings.Join(lines, "\n")
				return
			}
			done <- ""
		})
		if chat := <-done; chat != "" && strings.Contains(chat, "Error: ") {
			if !strings.Contains(chat, agent.ErrMaxTurnsReached.Error()) {
				t.Fatalf("chat shows an unexpected error:\n%s", chat)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run ended on an unanswered tool result without a visible error")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if last := m.agent.Messages()[len(m.agent.Messages())-1]; last.ToolResult == nil {
		t.Fatalf("precondition: the run should end on a tool result, got %+v", last)
	}
	cancel()
	<-loopDone
}
