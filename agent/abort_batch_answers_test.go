package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// An aborted tool batch answers every call of the batch (Pi 4438c58ba220, #9986): executeToolCallsSequential and
// executeToolCallsParallel no longer stop at an aborted signal (agent-loop.ts:540-646), and prepareToolCall answers
// "Operation aborted" before the tool lookup and the hooks (agent-loop.ts:716-725). Pi's own two cases are in
// agent_loop_upstream_test.go; these are the orders beside them, each with the whole event order a caller sees.

// batchTrace is the part of a run a session keeps or a client shows: the tool events, the messages and the turn ends.
func batchTrace(events []AgentEvent) []string {
	var lines []string
	text := func(content []ai.ToolResultMessageContent) string {
		var out strings.Builder
		for _, block := range content {
			if block, ok := block.(ai.TextContent); ok {
				out.WriteString(block.Text)
			}
		}
		return out.String()
	}
	for _, event := range events {
		switch event := event.(type) {
		case ToolExecutionStartEvent:
			lines = append(lines, "start "+event.ToolCallID)
		case ToolExecutionEndEvent:
			lines = append(lines, fmt.Sprintf("end %s %q isError=%v", event.ToolCallID, text(event.Result.Content), event.IsError))
		case MessageEndEvent:
			switch message := event.Message; {
			case message.ToolResult != nil:
				lines = append(lines, fmt.Sprintf("result %s %q isError=%v", message.ToolResult.ToolCallID, text(message.ToolResult.Content), message.ToolResult.IsError))
			case message.Assistant != nil:
				lines = append(lines, "assistant "+string(message.Assistant.StopReason))
			case message.User != nil:
				lines = append(lines, "user "+message.User.Content.(ai.UserContentBlocks)[0].(ai.TextContent).Text)
			}
		case TurnEndEvent:
			ids := make([]string, len(event.ToolResults))
			for i, result := range event.ToolResults {
				ids[i] = result.ToolCallID
			}
			lines = append(lines, "turn_end ["+strings.Join(ids, " ")+"]")
		case AgentEndEvent:
			lines = append(lines, "agent_end")
		}
	}
	return lines
}

func expectTrace(t *testing.T, events []AgentEvent, want []string) {
	t.Helper()
	if got := batchTrace(events); !reflect.DeepEqual(got, want) {
		t.Errorf("events:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// batchProbe is the tool and the hook of one batch: what executed and which calls the before hook was asked about.
type batchProbe struct {
	mu        sync.Mutex
	executed  []string
	hookCalls []string
}

func (p *batchProbe) tool(mode ToolExecutionMode, execute func(value string) (AgentToolResult, error)) *scriptTool {
	return &scriptTool{name: "slow", label: "Slow", description: "Slow tool", params: valueSchema, mode: mode,
		execute: func(_ context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
			value := argValue(args)
			p.mu.Lock()
			p.executed = append(p.executed, value)
			p.mu.Unlock()
			if execute != nil {
				return execute(value)
			}
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: value}}, Details: map[string]any{"value": value}}, nil
		}}
}

func (p *batchProbe) config(mode ToolExecutionMode) AgentLoopConfig {
	return AgentLoopConfig{ToolExecution: mode, BeforeToolCall: func(_ context.Context, call BeforeToolCallContext) *BeforeToolCallResult {
		p.hookCalls = append(p.hookCalls, call.ToolCall.ID)
		return nil
	}}
}

func (p *batchProbe) expect(t *testing.T, executed, hookCalls []string) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if !reflect.DeepEqual(p.executed, executed) {
		t.Errorf("executed = %v, want %v", p.executed, executed)
	}
	if !reflect.DeepEqual(p.hookCalls, hookCalls) {
		t.Errorf("before hook asked about %v, want %v", p.hookCalls, hookCalls)
	}
}

const (
	batchOpening = "user run three|assistant toolUse"
	batchClosing = "turn_end [tool-1 tool-2 tool-3]|assistant aborted|turn_end []|agent_end"
)

func traceOf(parts ...string) []string { return strings.Split(strings.Join(parts, "|"), "|") }

func abortedCall(id string) string {
	return fmt.Sprintf("start %s|end %s %q isError=true|result %s %q isError=true", id, id, "Operation aborted", id, "Operation aborted")
}

// The signal is aborted after the assistant message and before the first call starts: no tool runs, no hook is asked,
// and each call still has its start, its end and its result.
func TestAbortBeforeTheFirstCallOfABatchAnswersEveryCall(t *testing.T) {
	for _, mode := range []ToolExecutionMode{ToolModeSequential, ToolModeParallel} {
		t.Run(string(mode), func(t *testing.T) {
			signal, abort := context.WithCancel(context.Background())
			defer abort()
			var probe batchProbe
			events, messages := runObservedToolBatch(signal, t, probe.tool("", nil), probe.config(mode), func(event AgentEvent) {
				if end, ok := event.(MessageEndEvent); ok && end.Message.Assistant != nil && end.Message.Assistant.StopReason == ai.StopReasonToolUse {
					abort()
				}
			})
			probe.expect(t, nil, nil)
			want := traceOf(batchOpening, abortedCall("tool-1"), abortedCall("tool-2"), abortedCall("tool-3"), batchClosing)
			if mode == ToolModeParallel {
				// A parallel batch emits every start and end first, then the results in source order (agent-loop.ts:592-646).
				want = traceOf(batchOpening,
					`start tool-1|end tool-1 "Operation aborted" isError=true`, `start tool-2|end tool-2 "Operation aborted" isError=true`, `start tool-3|end tool-3 "Operation aborted" isError=true`,
					`result tool-1 "Operation aborted" isError=true|result tool-2 "Operation aborted" isError=true|result tool-3 "Operation aborted" isError=true`, batchClosing)
			}
			expectTrace(t, events, want)
			// The first message declares the tool (declareToolChanges).
			if got := roles(messages); !reflect.DeepEqual(got, []string{"system", "user", "assistant", "toolResult", "toolResult", "toolResult", "assistant"}) {
				t.Errorf("messages = %v, want a result for each of the three calls", got)
			}
		})
	}
}

// The first call of a sequential batch runs and fails while the user aborts (Pi's first case): the whole order.
func TestAbortDuringACallOfASequentialBatchAnswersTheRestInOrder(t *testing.T) {
	signal, abort := context.WithCancel(context.Background())
	defer abort()
	var probe batchProbe
	tool := probe.tool(ToolModeSequential, func(string) (AgentToolResult, error) {
		abort()
		return AgentToolResult{}, errors.New("interrupted")
	})
	events, _ := runToolBatch(signal, t, tool, probe.config(""))
	probe.expect(t, []string{"first"}, []string{"tool-1"})
	expectTrace(t, events, traceOf(batchOpening,
		`start tool-1|end tool-1 "interrupted" isError=true|result tool-1 "interrupted" isError=true`,
		abortedCall("tool-2"), abortedCall("tool-3"), batchClosing))
}

// The first call of a sequential batch succeeds and the user aborts before the second starts: the first result stays as
// it was and the two calls that never ran are answered.
func TestAbortBetweenTwoCallsOfASequentialBatchAnswersTheRest(t *testing.T) {
	signal, abort := context.WithCancel(context.Background())
	defer abort()
	var probe batchProbe
	events, _ := runObservedToolBatch(signal, t, probe.tool(ToolModeSequential, nil), probe.config(""), func(event AgentEvent) {
		if end, ok := event.(MessageEndEvent); ok && end.Message.ToolResult != nil && end.Message.ToolResult.ToolCallID == "tool-1" {
			abort()
		}
	})
	probe.expect(t, []string{"first"}, []string{"tool-1"})
	expectTrace(t, events, traceOf(batchOpening,
		`start tool-1|end tool-1 "first" isError=false|result tool-1 "first" isError=false`,
		abortedCall("tool-2"), abortedCall("tool-3"), batchClosing))
}

// A hook aborts while it decides about the second call of a parallel batch (Pi's second case): the whole order. The
// second and third calls end in the preparation loop; the first, prepared before the abort, ends when the prepared
// calls run (agent-loop.ts:622-631), and the results follow in source order.
func TestAbortWhileAParallelBatchIsPreparedAnswersEveryCallInOrder(t *testing.T) {
	signal, abort := context.WithCancel(context.Background())
	defer abort()
	var probe batchProbe
	config := probe.config("")
	asked := config.BeforeToolCall
	config.BeforeToolCall = func(ctx context.Context, call BeforeToolCallContext) *BeforeToolCallResult {
		if call.ToolCall.ID == "tool-2" {
			abort()
		}
		return asked(ctx, call)
	}
	events, _ := runToolBatch(signal, t, probe.tool("", nil), config)
	probe.expect(t, nil, []string{"tool-1", "tool-2"})
	expectTrace(t, events, traceOf(batchOpening,
		`start tool-1`, `start tool-2|end tool-2 "Operation aborted" isError=true`, `start tool-3|end tool-3 "Operation aborted" isError=true`,
		`end tool-1 "Operation aborted" isError=true`,
		`result tool-1 "Operation aborted" isError=true|result tool-2 "Operation aborted" isError=true|result tool-3 "Operation aborted" isError=true`, batchClosing))
}

// The user aborts once every call of a parallel batch is running: each call ends with what its tool returned, and no
// call is left out.
func TestAbortWhileEveryCallOfAParallelBatchRunsKeepsEachCallsOwnResult(t *testing.T) {
	signal, abort := context.WithCancel(context.Background())
	defer abort()
	var probe batchProbe
	var entered sync.WaitGroup
	entered.Add(3)
	tool := probe.tool("", func(value string) (AgentToolResult, error) {
		entered.Done()
		entered.Wait()
		if value == "first" {
			abort()
		}
		<-signal.Done()
		return AgentToolResult{}, errors.New(value + " stopped")
	})
	_, messages := runToolBatch(signal, t, tool, probe.config(""))
	want := [][2]string{{"tool-1", "first stopped"}, {"tool-2", "second stopped"}, {"tool-3", "third stopped"}}
	if got := toolResultTexts(messages); !reflect.DeepEqual(got, want) {
		t.Errorf("tool results = %v, want %v", got, want)
	}
}

// A steering message queued while the aborted batch ran is delivered after the results of the whole batch, so it never
// follows a call without a result.
func TestSteeringQueuedDuringAnAbortedBatchFollowsAResultForEveryCall(t *testing.T) {
	signal, abort := context.WithCancel(context.Background())
	defer abort()
	var probe batchProbe
	tool := probe.tool(ToolModeSequential, func(string) (AgentToolResult, error) {
		abort()
		return AgentToolResult{}, errors.New("interrupted")
	})
	config := probe.config("")
	queued := []AgentMessage{userMessage("interrupt")}
	config.GetSteeringMessages = func() []AgentMessage {
		probe.mu.Lock()
		defer probe.mu.Unlock()
		if len(probe.executed) == 0 {
			return nil
		}
		messages := queued
		queued = nil
		return messages
	}
	events, _ := runToolBatch(signal, t, tool, config)
	expectTrace(t, events, traceOf(batchOpening,
		`start tool-1|end tool-1 "interrupted" isError=true|result tool-1 "interrupted" isError=true`,
		abortedCall("tool-2"), abortedCall("tool-3"),
		"turn_end [tool-1 tool-2 tool-3]|user interrupt|assistant aborted|turn_end []|agent_end"))
}

// A before hook that stops waiting because the run was aborted returns the context's own error: a tool_call handler in an
// extension's process is not awaited past the abort. The call's answer is the abort's "Operation aborted", as upstream awaits the
// hook and then finds the signal aborted (agent-loop.ts:747-753), not the text of the cancellation. An error the hook threw for
// a reason of its own is still the call's result under an aborted signal, as upstream's catch reports it.
func TestABeforeHookEndedByTheAbortAnswersOperationAborted(t *testing.T) {
	for name, tc := range map[string]struct {
		err  func(ctx context.Context) error
		want string
	}{
		"the hook returns the context's error": {func(ctx context.Context) error { return fmt.Errorf("event tool_call: %w", ctx.Err()) }, "Operation aborted"},
		"the hook returns an error of its own": {func(context.Context) error { return errors.New("policy unavailable") }, "policy unavailable"},
	} {
		t.Run(name, func(t *testing.T) {
			signal, abort := context.WithCancel(context.Background())
			defer abort()
			var probe batchProbe
			var asked []string
			config := AgentLoopConfig{BeforeToolCallHooks: []BeforeToolCallHook{func(ctx context.Context, id, _ string, _ json.RawMessage) ToolCallHookResult {
				asked = append(asked, id)
				abort()
				return ToolCallHookResult{Err: tc.err(ctx)}
			}}}
			_, messages := runToolBatch(signal, t, probe.tool("", nil), config)
			want := [][2]string{{"tool-1", tc.want}, {"tool-2", "Operation aborted"}, {"tool-3", "Operation aborted"}}
			if got := toolResultTexts(messages); !reflect.DeepEqual(got, want) {
				t.Errorf("tool results = %v, want %v", got, want)
			}
			if !reflect.DeepEqual(asked, []string{"tool-1"}) {
				t.Errorf("the hook was asked about %v, want [tool-1]", asked)
			}
			probe.expect(t, nil, nil)
		})
	}
}

// RunToolCall with an aborted signal answers "Operation aborted" before it looks the tool up, validates the arguments or
// asks a hook (agent-loop.ts:810-812: "An aborted `signal` is checked first, before the hooks run").
func TestRunToolCallAnswersAnAbortedSignalBeforeTheLookupAndTheHooks(t *testing.T) {
	signal, abort := context.WithCancel(context.Background())
	abort()
	for name, call := range map[string]AgentToolCall{
		"a known tool":      toolCall("call-1", "slow", ai.JsonObject{"value": "first"}),
		"an unknown tool":   toolCall("call-1", "missing", ai.JsonObject{"value": "first"}),
		"invalid arguments": toolCall("call-1", "slow", ai.JsonObject{"value": 5}),
	} {
		t.Run(name, func(t *testing.T) {
			var probe batchProbe
			outcome, err := RunToolCall(signal, call, RunToolCallOptions{Tools: []AgentTool{probe.tool("", nil)}, BeforeToolCall: probe.config("").BeforeToolCall})
			if err != nil {
				t.Fatal(err)
			}
			if got := outcome.Result.Text(); got != "Operation aborted" || !outcome.IsError || outcome.DurationMs != nil {
				t.Errorf("outcome = %q isError=%v durationMs=%v, want Operation aborted, an error, no duration", got, outcome.IsError, outcome.DurationMs)
			}
			if details, ok := outcome.Result.Details.(map[string]any); !ok || len(details) != 0 {
				t.Errorf("details = %#v, want an empty object", outcome.Result.Details)
			}
			probe.expect(t, nil, nil)
		})
	}
}

// A session written by a build before this change can hold the orphans: an assistant message with three calls, the result
// of the first, then the aborted response. Resuming it rewrites nothing. The agent's messages stay as they were stored, and
// the request gives each orphaned call the synthetic "No result provided" error result (transformMessages), so the provider
// still accepts the history.
func TestOrphanedCallsOfAnOlderBuildAreAnsweredInTheRequestAndNotInTheTranscript(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("PI_CACHE_RETENTION", "")
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()
	provider := ai.NewAnthropicProvider(ai.AnthropicConfig{APIKey: "sk-ant-api03-test", Model: "claude-haiku-4-5", BaseURL: server.URL})
	turn := func(stop ai.StopReason, content ...ai.AssistantContentBlock) AgentMessage {
		return AgentMessage{Assistant: &AssistantMessage{Role: RoleAssistant, API: ai.APIAnthropicMessages, Provider: "anthropic", ModelID: "claude-haiku-4-5", StopReason: stop, Content: content}}
	}
	stored := []AgentMessage{
		userMessage("run three"),
		turn(ai.StopReasonToolUse, toolCall("toolu_1", "slow", ai.JsonObject{"value": "first"}), toolCall("toolu_2", "slow", ai.JsonObject{"value": "second"}), toolCall("toolu_3", "slow", ai.JsonObject{"value": "third"})),
		{ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "toolu_1", ToolName: "slow", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "interrupted"}}, IsError: true}},
		turn(ai.StopReasonAborted),
	}
	var probe batchProbe
	rec := newEventRecorder(nil)
	a := mustNewAgent(AgentOptions{
		Model:   &ai.Model{ID: "claude-haiku-4-5", DisplayName: "claude-haiku-4-5", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 200_000}},
		Tools:   []AgentTool{probe.tool("", nil)},
		EventCh: rec.ch,
	})
	a.SetMessages(stored)
	if _, err := a.Send(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
	events := rec.stop()

	var wire []string
	messages, _ := body["messages"].([]any)
	for _, raw := range messages {
		message := raw.(map[string]any)
		blocks, ok := message["content"].([]any)
		if !ok {
			wire = append(wire, fmt.Sprintf("%v %v", message["role"], message["content"]))
			continue
		}
		for _, raw := range blocks {
			block := raw.(map[string]any)
			switch block["type"] {
			case "tool_use":
				wire = append(wire, fmt.Sprintf("tool_use %v", block["id"]))
			case "tool_result":
				text := block["content"]
				if parts, ok := text.([]any); ok && len(parts) == 1 {
					text = parts[0].(map[string]any)["text"]
				}
				wire = append(wire, fmt.Sprintf("tool_result %v %q is_error=%v", block["tool_use_id"], text, block["is_error"]))
			case "text":
				wire = append(wire, fmt.Sprintf("%v %v", message["role"], block["text"]))
			}
		}
	}
	want := []string{
		"user run three",
		"tool_use toolu_1", "tool_use toolu_2", "tool_use toolu_3",
		`tool_result toolu_1 "interrupted" is_error=true`,
		`tool_result toolu_2 "No result provided" is_error=true`,
		`tool_result toolu_3 "No result provided" is_error=true`,
		"user next",
	}
	if !reflect.DeepEqual(wire, want) {
		t.Errorf("request:\n  %s\nwant:\n  %s", strings.Join(wire, "\n  "), strings.Join(want, "\n  "))
	}

	// Nothing is rewritten: the stored messages are still the first four, and the run added the declaration of its tool, the
	// prompt and the response only.
	transcript := a.MessagesSnapshot()
	if got, wantRoles := roles(transcript), []string{"user", "assistant", "toolResult", "assistant", "system", "user", "assistant"}; !reflect.DeepEqual(got, wantRoles) {
		t.Errorf("transcript = %v, want %v", got, wantRoles)
	}
	for _, event := range events {
		if end, ok := event.(MessageEndEvent); ok && end.Message.ToolResult != nil {
			t.Errorf("the run emitted a tool result message (%s): a session would persist it", end.Message.ToolResult.ToolCallID)
		}
	}
}
