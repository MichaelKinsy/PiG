package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type upstreamWaitTool struct {
	started  chan struct{}
	released <-chan struct{}
}

func (*upstreamWaitTool) Name() string                           { return "wait" }
func (*upstreamWaitTool) Label() string                          { return "Wait" }
func (*upstreamWaitTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (*upstreamWaitTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "wait", Description: "Wait until released", Parameters: map[string]any{"type": "object"}}
}
func (tool *upstreamWaitTool) Execute(ctx context.Context, _ string, _ json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	close(tool.started)
	select {
	case <-tool.released:
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "released"}}, Details: map[string]any{}}, nil
	case <-ctx.Done():
		return agent.AgentToolResult{}, ctx.Err()
	}
}
func upstreamUserTexts(messages []agent.AgentMessage) []string {
	var texts []string
	for _, message := range messages {
		if message.User != nil {
			var parts []string
			for _, block := range message.ContentBlocks() {
				if text, ok := block.(ai.TextContent); ok {
					parts = append(parts, text.Text)
				}
			}
			texts = append(texts, strings.Join(parts, "\n"))
		}
	}
	return texts
}
func awaitUpstreamSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-t.Context().Done():
		t.Fatal("expected operation did not start")
	}
}

func TestAgentSettledUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6363-agent-settled-event.test.ts:29
	t.Run("emits one agent_settled event after automatic retry finishes", func(t *testing.T) {
		var extensionEvents, publicEvents []string
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"agent_end": {func(...any) (any, error) { extensionEvents = append(extensionEvents, "agent_end"); return nil, nil }},
			"agent_settled": {func(args ...any) (any, error) {
				idle, err := extension.FromContext(args[1].(context.Context)).IsIdle()
				if err != nil {
					return nil, err
				}
				extensionEvents = append(extensionEvents, fmt.Sprintf("agent_settled:%t", idle))
				return nil, nil
			}},
		}}
		h := newRecoveryHarness(t, harnessOptions{settings: `{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":1}}`, extension: ext}, fauxError("overloaded_error"), fauxReply("recovered", ai.StopReasonStop, 0))
		var willRetry []bool
		h.session.Subscribe(func(event agent.AgentEvent) {
			switch event := event.(type) {
			case agent.AgentEndEvent:
				willRetry = append(willRetry, event.WillRetry)
			case agent.AgentSettledEvent:
				publicEvents = append(publicEvents, "agent_settled")
			}
		})
		if _, err := h.session.Send(t.Context(), "test"); err != nil {
			t.Fatal(err)
		}
		if err := h.session.FlushEvents(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(willRetry, []bool{true, false}) {
			t.Fatalf("willRetry=%v", willRetry)
		}
		if !reflect.DeepEqual(extensionEvents, []string{"agent_end", "agent_end", "agent_settled:true"}) {
			t.Fatalf("extension events=%q", extensionEvents)
		}
		if !reflect.DeepEqual(publicEvents, []string{"agent_settled"}) {
			t.Fatalf("public events=%q", publicEvents)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6363-agent-settled-event.test.ts:64
	t.Run("settles only after follow-ups queued by agent_end handlers run", func(t *testing.T) {
		var queued bool
		var idleStates []bool
		var session *Session
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"agent_end": {func(...any) (any, error) {
				if !queued {
					queued = true
					return nil, session.SendUserMessage(t.Context(), "status follow-up", &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAsFollowUp})
				}
				return nil, nil
			}},
			"agent_settled": {func(args ...any) (any, error) {
				idle, err := extension.FromContext(args[1].(context.Context)).IsIdle()
				if err == nil {
					idleStates = append(idleStates, idle)
				}
				return nil, err
			}},
		}}
		h := newRecoveryHarness(t, harnessOptions{extension: ext}, fauxReply("first", ai.StopReasonStop, 0), fauxReply("second", ai.StopReasonStop, 0))
		session = h.session
		ends, settles := 0, 0
		session.Subscribe(func(event agent.AgentEvent) {
			switch event.(type) {
			case agent.AgentEndEvent:
				ends++
			case agent.AgentSettledEvent:
				settles++
			}
		})
		if _, err := session.Send(t.Context(), "hello"); err != nil {
			t.Fatal(err)
		}
		if err := session.FlushEvents(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := upstreamUserTexts(session.Messages()); !reflect.DeepEqual(got, []string{"hello", "status follow-up"}) {
			t.Fatalf("user texts=%q", got)
		}
		if ends != 2 || settles != 1 || !reflect.DeepEqual(idleStates, []bool{true}) {
			t.Fatalf("ends=%d settles=%d idle=%v", ends, settles, idleStates)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6363-agent-settled-event.test.ts:92
	t.Run("extension command waitForIdle waits for session-level settlement", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			tool := &upstreamWaitTool{started: make(chan struct{}), released: release}
			started := make(chan struct{})
			var commandResults []bool
			ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{}, Commands: map[string]extension.RegisteredCommand{"after-idle": {Name: "after-idle", Description: "Wait for idle", Handler: func(ctx context.Context, _ string) error {
				close(started)
				command := extension.CommandContextFromContext(ctx)
				if err := command.WaitForIdle(); err != nil {
					return err
				}
				idle, err := command.IsIdle()
				if err == nil {
					commandResults = append(commandResults, idle)
				}
				return err
			}}}}
			h := newRecoveryHarness(t, harnessOptions{extension: ext, tools: []agent.AgentTool{tool}}, fauxToolCall("wait"), fauxReply("done", ai.StopReasonStop, 0))
			settles := 0
			h.session.Subscribe(func(event agent.AgentEvent) {
				if _, ok := event.(agent.AgentSettledEvent); ok {
					settles++
				}
			})
			promptDone := make(chan error, 1)
			go func() { _, err := h.session.Send(t.Context(), "start"); promptDone <- err }()
			awaitUpstreamSignal(t, tool.started)
			commandDone := make(chan error, 1)
			go func() {
				if !h.session.currentRunner().ExecuteCommand(t.Context(), "after-idle", "") {
					commandDone <- fmt.Errorf("after-idle command not found")
					return
				}
				commandDone <- nil
			}()
			awaitUpstreamSignal(t, started)
			synctest.Wait()
			select {
			case err := <-commandDone:
				t.Fatalf("command completed before release: %v", err)
			default:
			}
			once.Do(func() { close(release) })
			if err := <-promptDone; err != nil {
				t.Fatal(err)
			}
			if err := <-commandDone; err != nil {
				t.Fatal(err)
			}
			if err := h.session.FlushEvents(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(commandResults, []bool{true}) || settles != 1 {
				t.Fatalf("command results=%v settled=%d", commandResults, settles)
			}
		})
	})
}
