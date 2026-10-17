package pipelinediff

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// funcTool is a tool whose Execute is a function.
type funcTool struct {
	name string
	run  func(ctx context.Context) (agent.AgentToolResult, error)
}

func (t funcTool) Name() string  { return t.name }
func (t funcTool) Label() string { return t.name }
func (t funcTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: t.name, Description: t.name, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (t funcTool) Execute(ctx context.Context, _ string, _ json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return t.run(ctx)
}
func (funcTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }

// TestAbortMidToolThenSendIsValid aborts a turn while one of three parallel tool calls still runs (the others finished, so their results exist and the third has none), then sends again. The request after the abort, and the one after a second abort of a lone call, must keep every tool call answered directly after its assistant message (Anthropic Messages tool_result contract; Pi: packages/ai/src/api/transform-messages.ts closePendingToolCalls) and meet each provider contract.
func TestAbortMidToolThenSendIsValid(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: home})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	collector := requestvalidity.CollectAll(t)

	for _, shape := range []struct {
		name  string
		calls []string
	}{
		{"parallel, one still running", []string{"fast", "block", "fast"}},
		{"lone running call", []string{"block"}},
		{"running call first", []string{"block", "fast"}},
	} {
		t.Run(shape.name, func(t *testing.T) {
			started := make(chan struct{}, 1)
			faux := ai.NewFauxProvider(ai.FauxConfig{})
			blocks := make([]ai.FauxContentBlock, 0, len(shape.calls))
			for _, name := range shape.calls {
				blocks = append(blocks, ai.FauxToolCall(name, map[string]any{}, nil))
			}
			faux.SetResponses([]ai.FauxResponseStep{
				ai.FauxStaticStep(ai.FauxResponse{Content: blocks, StopReason: "toolUse"}),
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("after abort")}, StopReason: "stop"}),
			})
			provider := &recordingProvider{FauxProviderHandle: faux}
			model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 1000000, SupportsToolUse: true}}
			tools := []agent.AgentTool{
				funcTool{"fast", func(context.Context) (agent.AgentToolResult, error) {
					return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}}, nil
				}},
				funcTool{"block", func(ctx context.Context) (agent.AgentToolResult, error) {
					select {
					case started <- struct{}{}:
					default:
					}
					<-ctx.Done()
					return agent.AgentToolResult{}, ctx.Err()
				}},
			}
			s, err := coding.NewSession(services, coding.SessionOptions{Model: model, SkipBuiltinTools: true, Tools: tools})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			// The Session blocks once nobody drains its event channel.
			go func() {
				for range s.Events() {
				}
			}()

			first := make(chan error, 1)
			go func() { _, err := s.Send(context.Background(), "run the tools"); first <- err }()
			select {
			case <-started:
			case <-time.After(30 * time.Second):
				t.Fatal("the blocking tool never started")
			}
			if err := s.Abort(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-first:
			case <-time.After(30 * time.Second):
				t.Fatal("the aborted Send did not return")
			}
			seen := len(collector.Violations())
			if _, err := s.Send(context.Background(), "continue after the abort"); err != nil {
				t.Fatalf("Send after abort: %v", err)
			}
			// Pi's agent loop continues after the aborted tool batch and streams the follow-up with the aborted signal (agent-loop.ts:242, :270-273); a caller-owned provider the runtime does not know receives that request and answers it aborted (abort.test.ts:56-99, 1268115b91).
			if len(provider.requests) != 3 {
				t.Fatalf("provider requests = %d, want 3 (the tool-call turn; the aborted follow-up; the new prompt)", len(provider.requests))
			}
			for index, request := range provider.requests[1:] {
				if problem := handoffToolFlowError(request); problem != "" {
					t.Errorf("request %d after the abort hands the provider a broken tool flow: %s", index+2, problem)
				}
			}
			if violations := collector.Violations()[seen:]; len(violations) > 0 {
				t.Errorf("the request after the abort breaks a provider contract: %v", violations)
			}

			// Navigate to every entry of the aborted turn's tree, with a branch summary, and send again.
			var entryIDs []string
			for _, entry := range s.Entries() {
				entryIDs = append(entryIDs, entry.Base().ID)
			}
			for _, id := range entryIDs {
				provider.SetResponses([]ai.FauxResponseStep{
					ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("summary")}, StopReason: "stop"}),
					ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("answer")}, StopReason: "stop"}),
				})
				if _, err := s.NavigateTree(context.Background(), id, coding.NavigateTreeOptions{Summarize: true}); err != nil {
					continue
				}
				before, seen := len(provider.requests), len(collector.Violations())
				if _, err := s.Send(context.Background(), "after navigating"); err != nil {
					t.Fatalf("Send after navigating to %s: %v", id, err)
				}
				for _, request := range provider.requests[before:] {
					if problem := handoffToolFlowError(request); problem != "" {
						t.Errorf("navigating to %s after the abort: the agent handed the provider a broken tool flow: %s", id, problem)
					}
				}
				if violations := collector.Violations()[seen:]; len(violations) > 0 {
					t.Errorf("navigating to %s after the abort: the request breaks a provider contract: %v", id, violations)
				}
			}
		})
	}
}
