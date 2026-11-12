package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsloop"
)

// taskEventName names an event in the log of a task-bounds test.
func taskEventName(ev AgentEvent) string {
	switch ev := ev.(type) {
	case AgentStartEvent:
		return "agent_start"
	case AgentEndEvent:
		return "agent_end"
	case TurnStartEvent:
		return "turn_start"
	case TurnEndEvent:
		return "turn_end"
	case MessageStartEvent:
		if ev.Message.Assistant == nil {
			return "prompt_start"
		}
		return "message_start"
	case MessageUpdateEvent:
		return "message_update"
	case MessageEndEvent:
		if ev.Message.Assistant == nil {
			return "prompt_end"
		}
		return "message_end"
	case ToolExecutionStartEvent:
		return "tool_execution_start:" + ev.ToolCallID
	case ToolExecutionUpdateEvent:
		return "tool_execution_update:" + ev.ToolCallID
	case ToolExecutionEndEvent:
		return "tool_execution_end:" + ev.ToolCallID
	}
	return fmt.Sprintf("%T", ev)
}

// taskBounds builds an agent whose run reports the tasks of Pi's event loop it executes (jsloop.Task), and one log of the task bounds and the events a listener receives, in the order they happen. parked is closed when the first task ends.
func taskBounds(opts AgentOptions) (a *Agent, log *orderLog, ctx context.Context, parked <-chan struct{}) {
	log = &orderLog{}
	a = mustNewAgent(opts)
	a.Subscribe(func(_ context.Context, ev AgentEvent) error {
		log.add(taskEventName(ev))
		return nil
	})
	firstEnd := make(chan struct{})
	var once sync.Once
	task := jsloop.NewTask(func(running bool) {
		if running {
			log.add("task{")
			return
		}
		log.add("}task")
		once.Do(func() { close(firstEnd) })
	})
	return a, log, jsloop.WithTask(context.Background(), task), firstEnd
}

func textPartial(text string) *ai.AssistantMessage {
	return agentTestAssistant([]ai.AssistantContentBlock{ai.TextContent{Text: text}}, ai.StopReasonPending)
}

// Pi's loop handles the events of a response its provider delivered in one run, and then emits message_end, turn_end and agent_end back to back without awaiting anything (packages/agent/src/agent-loop.ts:262-263, 296-299, 329): one task, in which no timer callback runs. The run reports that stretch as one task, from the response's first event to agent_end.
func TestTaskSpansAResponseAndTheLoopEndEvents(t *testing.T) {
	a, log, ctx, _ := taskBounds(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream {
		return agentTestStream([]ai.AssistantMessageEvent{
			ai.StartEvent{Partial: agentTestAssistant(nil, ai.StopReasonPending)},
			ai.TextStartEvent{ContentIndex: 0, Partial: textPartial("")},
			ai.TextDeltaEvent{ContentIndex: 0, Delta: "hi", Partial: textPartial("hi")},
			ai.TextEndEvent{ContentIndex: 0, Content: "hi", Partial: textPartial("hi")},
			ai.DoneEvent{Reason: ai.StopReasonStop, Message: textMessage("hi")},
		})
	}})})
	if _, err := a.Send(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"agent_start", "turn_start", "prompt_start", "prompt_end",
		"task{", "message_start", "message_update", "message_update", "message_update", "message_end", "turn_end", "agent_end", "}task",
	}
	if got := log.list(); !slices.Equal(got, want) {
		t.Fatalf("events and task bounds = %q, want %q", got, want)
	}
}

// A task ends where the reader of the response would wait for the provider: nothing ready is left, so Pi's loop turns and its timers run. A streamed delta therefore waits for nothing but the rest of what the provider had delivered with it.
func TestTaskEndsWhereTheReaderWaitsForTheProvider(t *testing.T) {
	stream := ai.NewAssistantMessageEventStream()
	push := func(events ...ai.AssistantMessageEvent) {
		t.Helper()
		for _, event := range events {
			if err := stream.Push(event); err != nil {
				t.Fatal(err)
			}
		}
	}
	push(
		ai.StartEvent{Partial: agentTestAssistant(nil, ai.StopReasonPending)},
		ai.TextStartEvent{ContentIndex: 0, Partial: textPartial("")},
		ai.TextDeltaEvent{ContentIndex: 0, Delta: "hi", Partial: textPartial("hi")},
	)
	a, log, ctx, parked := taskBounds(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream { return stream }})})
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := a.Send(ctx, "run"); err != nil {
			t.Errorf("Send: %v", err)
		}
	}()
	waitSignal(t, parked, "the task to end while the provider has delivered nothing more")
	want := []string{"agent_start", "turn_start", "prompt_start", "prompt_end", "task{", "message_start", "message_update", "message_update", "}task"}
	if got := log.list(); !slices.Equal(got, want) {
		t.Fatalf("with the provider waiting, events and task bounds = %q, want %q", got, want)
	}
	push(
		ai.TextEndEvent{ContentIndex: 0, Content: "hi", Partial: textPartial("hi")},
		ai.DoneEvent{Reason: ai.StopReasonStop, Message: textMessage("hi")},
	)
	waitSignal(t, done, "the run")
	want = append(want, "task{", "message_update", "message_end", "turn_end", "agent_end", "}task")
	if got := log.list(); !slices.Equal(got, want) {
		t.Fatalf("events and task bounds = %q, want %q", got, want)
	}
}

// Pi emits the tool_execution_start of every call of a parallel batch, and prepares each, before any tool runs (agent-loop.ts executeToolCalls), in the task of the response that asked for them. The task ends where the first tool starts its work, which is real work the loop awaits.
func TestTaskHoldsEveryToolStartAndEndsBeforeAToolRuns(t *testing.T) {
	var log *orderLog
	tool := &scriptTool{name: "p", mode: ToolModeParallel, params: map[string]any{"type": "object"},
		execute: func(_ context.Context, id string, _ json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
			log.add("exec:" + id)
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: id}}}, nil
		}}
	a, taskLog, ctx, _ := taskBounds(AgentOptions{
		Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(toolCall("call-1", "p", nil), toolCall("call-2", "p", nil))}),
		Tools: []AgentTool{tool},
	})
	log = taskLog
	if _, err := a.Send(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	got := log.list()
	want := []string{"task{", "message_start", "message_end", "tool_execution_start:call-1", "tool_execution_start:call-2", "}task"}
	first := slices.Index(got, "task{")
	if first < 0 || len(got) < first+len(want) || !slices.Equal(got[first:first+len(want)], want) {
		t.Fatalf("events and task bounds = %q, want the first task to be %q", got, want)
	}
	for _, exec := range []string{"exec:call-1", "exec:call-2"} {
		if at := slices.Index(got, exec); at < first+len(want) {
			t.Fatalf("%s is at %d, inside or before the task that ends at %d: %q", exec, at, first+len(want)-1, got)
		}
	}
}

// A queued follow-up continues the run with another turn, whose preparation may await real work (a compaction, a credential, the request). The task of the response before it ends ahead of that preparation, not at the next thing that happens to end one.
func TestTaskEndsBeforeAFollowUpTurnIsPrepared(t *testing.T) {
	a, log, ctx, _ := taskBounds(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("ok")})})
	a.FollowUp(userMessage("more"))
	if _, err := a.Send(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"agent_start", "turn_start", "prompt_start", "prompt_end",
		"task{", "message_start", "message_end", "turn_end", "}task",
		"turn_start", "prompt_start", "prompt_end",
		"task{", "message_start", "message_end", "turn_end", "agent_end", "}task",
	}
	if got := log.list(); !slices.Equal(got, want) {
		t.Fatalf("events and task bounds = %q, want %q", got, want)
	}
}

// A run whose owner asked for no tasks reports none and reads its response as before.
func TestRunWithoutATaskReportsNone(t *testing.T) {
	a, log, _, _ := taskBounds(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("ok")})})
	if _, err := a.Send(context.Background(), "run"); err != nil {
		t.Fatal(err)
	}
	want := []string{"agent_start", "turn_start", "prompt_start", "prompt_end", "message_start", "message_end", "turn_end", "agent_end"}
	if got := log.list(); !slices.Equal(got, want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
}
