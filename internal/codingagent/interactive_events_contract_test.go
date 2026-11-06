package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts handleEvent (3467-3865): one test group per event type.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui/widthx"
	"github.com/MichaelKinsy/PiG/tui"
)

// eventProbe is a mounted interactive mode with the terminal progress calls recorded.
type eventProbe struct {
	*InteractiveMode
	progress *[]bool
}

func newEventProbe(t *testing.T, showProgress bool) eventProbe {
	t.Helper()
	m, _ := newTickRenderProbe(t, "regular")
	m.opts.Settings.ShowTerminalProgress = &showProgress
	calls := []bool{}
	previous := setTerminalProgress
	setTerminalProgress = func(active bool) { calls = append(calls, active) }
	t.Cleanup(func() { setTerminalProgress = previous })
	t.Cleanup(func() { m.clearStatusIndicator("") })
	return eventProbe{InteractiveMode: m, progress: &calls}
}

func (p eventProbe) chat() string {
	return widthx.StripAnsi(strings.Join(p.chatContainer.Render(100), "\n"))
}

func (p eventProbe) indicatorText() string {
	return widthx.StripAnsi(strings.Join(p.activeStatusIndicator.Render(100), "\n"))
}

func (p eventProbe) indicatorKind() string {
	if p.activeStatusIndicator == nil {
		return ""
	}
	return p.activeStatusIndicator.Kind
}

func startReadTool(p eventProbe, id string) {
	p.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: id, ToolName: "read", Args: json.RawMessage(`{"path":"` + id + `.txt"}`)})
}

func toolCallPartial(id, name string) *ai.AssistantMessage {
	return &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: id, Name: name}}}
}

// agent_start clears the pending tools.
func TestEventAgentStartClearsThePendingTools(t *testing.T) {
	p := newEventProbe(t, true)
	startReadTool(p, "a")
	p.handleAgentEvent(agent.MessageStartEvent{Message: assistantMsg("")})
	p.handleAgentEvent(agent.MessageUpdateEvent{Message: assistantMsg(""), AssistantMessageEvent: ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: `{}`, Partial: toolCallPartial("b", "read")}})
	if len(p.toolByID) == 0 || len(p.toolStarts) == 0 || len(p.toolFileCalls) == 0 || len(p.pendingArgs) == 0 {
		t.Fatalf("precondition: toolByID=%d toolStarts=%d toolFileCalls=%d pendingArgs=%d", len(p.toolByID), len(p.toolStarts), len(p.toolFileCalls), len(p.pendingArgs))
	}
	p.handleAgentEvent(agent.AgentStartEvent{})
	if len(p.toolByID) != 0 || len(p.toolStarts) != 0 || len(p.toolFileCalls) != 0 || len(p.pendingArgs) != 0 {
		t.Fatalf("pending tools survive agent_start: toolByID=%d toolStarts=%d toolFileCalls=%d pendingArgs=%d", len(p.toolByID), len(p.toolStarts), len(p.toolFileCalls), len(p.pendingArgs))
	}
}

// turn_start reports terminal progress when enabled, shows the working status when it is visible without replacing one already shown, and
// clears the status when it is not visible.
func TestEventTurnStartShowsProgressAndTheWorkingStatus(t *testing.T) {
	p := newEventProbe(t, true)
	p.workingVisible = true
	p.handleAgentEvent(agent.TurnStartEvent{})
	if !slices.Equal(*p.progress, []bool{true}) || p.indicatorKind() != "working" {
		t.Fatalf("progress=%v status=%q, want [true] and working", *p.progress, p.indicatorKind())
	}
	shown := p.activeStatusIndicator
	p.handleAgentEvent(agent.TurnStartEvent{})
	if p.activeStatusIndicator != shown {
		t.Fatal("a second turn_start replaced the working status")
	}
	p.showCompactionStatusIndicator("manual")
	p.handleAgentEvent(agent.TurnStartEvent{})
	if p.indicatorKind() != "working" {
		t.Fatalf("turn_start left the %q status over the working one", p.indicatorKind())
	}
	p.workingVisible = false
	p.handleAgentEvent(agent.TurnStartEvent{})
	if p.indicatorKind() != "" {
		t.Fatalf("a hidden working status left %q shown", p.indicatorKind())
	}
	off := newEventProbe(t, false)
	off.handleAgentEvent(agent.TurnStartEvent{})
	if len(*off.progress) != 0 {
		t.Fatalf("progress %v reported with the setting off", *off.progress)
	}
}

// agent_end stops the terminal progress, clears the working status only, removes a message still streaming, and clears the pending tools.
func TestEventAgentEndClearsTheRunState(t *testing.T) {
	p := newEventProbe(t, true)
	p.workingVisible = true
	p.handleAgentEvent(agent.TurnStartEvent{})
	p.handleAgentEvent(agent.MessageStartEvent{Message: assistantMsg("", ai.TextContent{Text: "STREAMING"})})
	startReadTool(p, "a")
	if !strings.Contains(p.chat(), "STREAMING") || p.evCurrentBlock == nil {
		t.Fatalf("precondition: chat %q", p.chat())
	}
	p.handleAgentEvent(agent.AgentEndEvent{})
	if got := *p.progress; !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("progress = %v, want [true false]", got)
	}
	if p.indicatorKind() != "" || p.evCurrentBlock != nil || strings.Contains(p.chat(), "STREAMING") || len(p.assistantBlocks) != 0 {
		t.Fatalf("status=%q block=%v chat=%q blocks=%d, want the run state cleared", p.indicatorKind(), p.evCurrentBlock, p.chat(), len(p.assistantBlocks))
	}
	if len(p.toolByID) != 0 || len(p.pendingArgs) != 0 {
		t.Fatalf("pending tools survive agent_end: %d %d", len(p.toolByID), len(p.pendingArgs))
	}
	p.showCompactionStatusIndicator("manual")
	p.handleAgentEvent(agent.AgentEndEvent{})
	if p.indicatorKind() != "compaction" {
		t.Fatalf("agent_end cleared the %q status, want only the working one cleared", p.indicatorKind())
	}
}

// message_start renders a user message, a displayed custom message and an assistant message, which streams into one block that later events update.
func TestEventMessageStartRendersEachRole(t *testing.T) {
	p := newEventProbe(t, false)
	p.handleAgentEvent(agent.MessageStartEvent{Message: userMsg("USER TEXT")})
	if !strings.Contains(p.chat(), "USER TEXT") {
		t.Fatalf("user message missing: %q", p.chat())
	}
	before := p.chat()
	p.handleAgentEvent(agent.MessageStartEvent{Message: userMsg("")})
	if p.chat() != before {
		t.Fatalf("an empty user message changed the chat: %q", p.chat())
	}
	p.handleAgentEvent(agent.MessageStartEvent{Message: agent.AgentMessage{Custom: map[string]any{"customType": "note", "content": "SHOWN CUSTOM", "display": true}}})
	p.handleAgentEvent(agent.MessageStartEvent{Message: agent.AgentMessage{Custom: map[string]any{"customType": "note", "content": "HIDDEN CUSTOM", "display": false}}})
	if got := p.chat(); !strings.Contains(got, "SHOWN CUSTOM") || strings.Contains(got, "HIDDEN CUSTOM") {
		t.Fatalf("custom messages: %q", got)
	}
	p.handleAgentEvent(agent.MessageStartEvent{Message: assistantMsg("", ai.TextContent{Text: "ASSISTANT TEXT"})})
	if got := p.chat(); !strings.Contains(got, "ASSISTANT TEXT") || p.evCurrentBlock == nil || !slices.Contains(p.assistantBlocks, p.evCurrentBlock) {
		t.Fatalf("assistant message: %q block=%v", got, p.evCurrentBlock)
	}
	if !slices.Contains(p.chatContainer.Children(), tui.Component(p.evCurrentBlock)) {
		t.Fatal("the assistant block is not in the chat")
	}
}

// message_update refreshes the streaming block; a tool call it carries creates one card for its id, marks the block as having tool calls, and
// later updates reuse the card.
func TestEventMessageUpdateStreamsTextAndCreatesOneCardPerToolCall(t *testing.T) {
	p := newEventProbe(t, false)
	msg := assistantMsg("", ai.TextContent{Text: "first"})
	p.handleAgentEvent(agent.MessageStartEvent{Message: msg})
	msg.Assistant.Content = []ai.AssistantContentBlock{ai.TextContent{Text: "second"}}
	p.handleAgentEvent(agent.MessageUpdateEvent{Message: msg, AssistantMessageEvent: ai.TextDeltaEvent{ContentIndex: 0, Delta: "second"}})
	if got := p.chat(); !strings.Contains(got, "second") || strings.Contains(got, "first") {
		t.Fatalf("streamed text: %q", got)
	}
	if strings.Contains(strings.Join(p.evCurrentBlock.Render(80), ""), "\x1b]133;A") == false {
		t.Fatal("precondition: a block without tool calls is zoned")
	}
	for range 2 {
		p.handleAgentEvent(agent.MessageUpdateEvent{Message: msg, AssistantMessageEvent: ai.ToolCallDeltaEvent{ContentIndex: 1, Delta: `{"path":"x.txt"}`, Partial: &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "second"}, ai.ToolCall{ID: "call-1", Name: "read", Arguments: map[string]any{"path": "x.txt"}}}}}})
	}
	if len(p.toolByID) != 1 || len(p.toolOrder) != 1 || strings.Count(p.chat(), "read") != 1 {
		t.Fatalf("toolByID=%d toolOrder=%d chat=%q, want one card", len(p.toolByID), len(p.toolOrder), p.chat())
	}
	if strings.Contains(strings.Join(p.evCurrentBlock.Render(80), ""), "\x1b]133;A") {
		t.Fatal("a block with tool calls is not zoned")
	}
	p.toolsExpanded = true
	p.handleAgentEvent(agent.MessageUpdateEvent{Message: msg, AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 0, Partial: toolCallPartial("call-2", "read")}})
	if len(p.toolByID) != 2 || p.toolByID["call-2"].Collapsed || !p.toolByID["call-1"].Collapsed {
		t.Fatalf("a card created while tools are expanded starts expanded, an earlier one is kept: call-2 collapsed=%v call-1 collapsed=%v", p.toolByID["call-2"].Collapsed, p.toolByID["call-1"].Collapsed)
	}
}

// message_end finishes the streaming block: an aborted message carries the abort text, an aborted or failed message pushes its error into every
// pending tool card, a normal one marks their arguments complete, and the pending arguments and the block are cleared.
func TestEventMessageEndFinishesTheMessage(t *testing.T) {
	text := func(p eventProbe) string { return p.chat() }
	t.Run("aborted", func(t *testing.T) {
		p := newEventProbe(t, false)
		msg := assistantMsg("", ai.ToolCall{ID: "call-1", Name: "read"})
		p.handleAgentEvent(agent.MessageStartEvent{Message: msg})
		p.handleAgentEvent(agent.MessageUpdateEvent{Message: msg, AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 0, Partial: toolCallPartial("call-1", "read")}})
		msg.Assistant.StopReason = ai.StopReasonAborted
		p.handleAgentEvent(agent.MessageEndEvent{Message: msg})
		if got := text(p); !strings.Contains(got, "Operation aborted") {
			t.Fatalf("aborted tool card lacks the abort text: %q", got)
		}
		if len(p.toolByID) != 0 || len(p.pendingArgs) != 0 || p.evCurrentBlock != nil {
			t.Fatalf("toolByID=%d pendingArgs=%d block=%v, want them cleared", len(p.toolByID), len(p.pendingArgs), p.evCurrentBlock)
		}
	})
	t.Run("error without a message", func(t *testing.T) {
		p := newEventProbe(t, false)
		msg := assistantMsg("", ai.ToolCall{ID: "call-1", Name: "read"})
		p.handleAgentEvent(agent.MessageStartEvent{Message: msg})
		p.handleAgentEvent(agent.MessageUpdateEvent{Message: msg, AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 0, Partial: toolCallPartial("call-1", "read")}})
		msg.Assistant.StopReason = ai.StopReasonError
		p.handleAgentEvent(agent.MessageEndEvent{Message: msg})
		if got := text(p); !strings.Contains(got, "Error") {
			t.Fatalf("failed tool card lacks the default error text: %q", got)
		}
	})
	t.Run("error with a message", func(t *testing.T) {
		p := newEventProbe(t, false)
		msg := assistantMsg("", ai.ToolCall{ID: "call-1", Name: "read"})
		p.handleAgentEvent(agent.MessageStartEvent{Message: msg})
		p.handleAgentEvent(agent.MessageUpdateEvent{Message: msg, AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 0, Partial: toolCallPartial("call-1", "read")}})
		msg.Assistant.StopReason, msg.Assistant.ErrorMessage = ai.StopReasonError, "PROVIDER FAILED"
		p.handleAgentEvent(agent.MessageEndEvent{Message: msg})
		if got := text(p); !strings.Contains(got, "PROVIDER FAILED") {
			t.Fatalf("failed tool card lacks the provider error: %q", got)
		}
	})
	t.Run("normal", func(t *testing.T) {
		p := newEventProbe(t, false)
		msg := assistantMsg("", ai.ToolCall{ID: "call-1", Name: "read"})
		p.handleAgentEvent(agent.MessageStartEvent{Message: msg})
		p.handleAgentEvent(agent.MessageUpdateEvent{Message: msg, AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 0, Partial: toolCallPartial("call-1", "read")}})
		msg.Assistant.StopReason = ai.StopReasonToolUse
		p.handleAgentEvent(agent.MessageEndEvent{Message: msg})
		if len(p.toolByID) != 1 || len(p.pendingArgs) != 0 || p.evCurrentBlock != nil {
			t.Fatalf("toolByID=%d pendingArgs=%d block=%v, want the card kept and the arguments cleared", len(p.toolByID), len(p.pendingArgs), p.evCurrentBlock)
		}
	})
	t.Run("user message", func(t *testing.T) {
		p := newEventProbe(t, false)
		p.handleAgentEvent(agent.MessageStartEvent{Message: assistantMsg("")})
		p.handleAgentEvent(agent.MessageUpdateEvent{Message: assistantMsg(""), AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 0, Partial: toolCallPartial("call-1", "read")}})
		p.handleAgentEvent(agent.MessageEndEvent{Message: userMsg("hi")})
		if len(p.pendingArgs) != 1 {
			t.Fatal("a user message_end touched the pending tool arguments")
		}
	})
}

// A tool call made by another tool (a codemode script) is shown in its parent's row: its events create and update no card. Any other call gets one
// card, which a streamed call already created is reused, which later updates fill with partial results and the end settles.
func TestEventToolExecutionLifecycle(t *testing.T) {
	p := newEventProbe(t, false)
	p.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "child", ParentToolCallID: "parent", ToolName: "read", Args: json.RawMessage(`{"path":"c.txt"}`)})
	if len(p.toolByID) != 0 {
		t.Fatal("a nested call created a card")
	}
	p.toolsExpanded = true
	startReadTool(p, "a")
	card := p.toolByID["a"]
	if card == nil || card.Collapsed || p.toolStarts["a"].IsZero() || len(p.toolFileCalls) != 1 {
		t.Fatalf("a fresh start: card=%v starts=%v fileCalls=%d", card, p.toolStarts, len(p.toolFileCalls))
	}
	startReadTool(p, "a")
	if len(p.toolByID) != 1 || p.toolByID["a"] != card || strings.Count(p.chat(), "read a.txt") != 1 {
		t.Fatalf("a second start for the same id made another card: %q", p.chat())
	}
	p.handleAgentEvent(agent.ToolExecutionUpdateEvent{ToolCallID: "unknown", ToolName: "read"})
	p.handleAgentEvent(agent.ToolExecutionUpdateEvent{ToolCallID: "a", ToolName: "read", PartialResult: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "PARTIAL"}}, IsError: true}})
	if got := p.chat(); !strings.Contains(got, "PARTIAL") || (card.State == tui.ToolStateError) {
		t.Fatalf("a partial result is shown and is not an error: %q", got)
	}
	p.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "a", ToolName: "read", IsError: true, Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "FINAL"}}}})
	if got := p.chat(); !strings.Contains(got, "FINAL") || card.State != tui.ToolStateError || len(p.toolByID) != 0 || len(p.toolStarts) != 0 || len(p.toolFileCalls) != 0 {
		t.Fatalf("the end settles the card as an error and forgets the call: %q", got)
	}
	p.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "a", ToolName: "read", Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "AGAIN"}}}})
	if strings.Contains(p.chat(), "AGAIN") {
		t.Fatal("an end for a settled call drew a result")
	}
}

// compaction_start reports progress, shows the compaction status and marks the mode as compacting; compaction_end clears them, reports a
// cancellation or an error as the upstream branch does, and delivers the messages queued meanwhile.
func TestEventCompactionStartAndEnd(t *testing.T) {
	start := func(p eventProbe) {
		p.handleAgentEvent(agent.CompactionStartEvent{Reason: "manual"})
	}
	t.Run("start", func(t *testing.T) {
		p := newEventProbe(t, true)
		start(p)
		if !slices.Equal(*p.progress, []bool{true}) || p.indicatorKind() != "compaction" || !p.isCompacting {
			t.Fatalf("progress=%v status=%q compacting=%v", *p.progress, p.indicatorKind(), p.isCompacting)
		}
		off := newEventProbe(t, false)
		start(off)
		if len(*off.progress) != 0 {
			t.Fatalf("progress %v reported with the setting off", *off.progress)
		}
	})
	for _, tc := range []struct {
		name  string
		event agent.CompactionEndEvent
		want  string
	}{
		{"manual cancel", agent.CompactionEndEvent{Reason: "manual", Aborted: true}, "Error: Compaction cancelled"},
		{"auto cancel", agent.CompactionEndEvent{Reason: "threshold", Aborted: true}, "Auto-compaction cancelled"},
		{"manual error", agent.CompactionEndEvent{Reason: "manual", ErrorMessage: "BOOM"}, "Error: BOOM"},
		{"auto error", agent.CompactionEndEvent{Reason: "threshold", ErrorMessage: "BOOM"}, "BOOM"},
		{"aborted wins over an error", agent.CompactionEndEvent{Reason: "manual", Aborted: true, ErrorMessage: "BOOM"}, "Error: Compaction cancelled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newEventProbe(t, true)
			start(p)
			p.handleAgentEvent(tc.event)
			got := p.chat()
			if !strings.Contains(got, tc.want) || (tc.name != "auto error" && tc.name != "manual error" && strings.Contains(got, "BOOM")) {
				t.Fatalf("chat = %q, want %q", got, tc.want)
			}
			if tc.name == "auto error" && strings.Contains(got, "Error: BOOM") {
				t.Fatalf("an auto-compaction error carries no Error prefix: %q", got)
			}
			if !slices.Equal(*p.progress, []bool{true, false}) || p.indicatorKind() != "" || p.isCompacting {
				t.Fatalf("progress=%v status=%q compacting=%v, want them cleared", *p.progress, p.indicatorKind(), p.isCompacting)
			}
		})
	}
	t.Run("only the compaction status is cleared", func(t *testing.T) {
		p := newEventProbe(t, false)
		p.showRetryStatusIndicator(1, 3, 5000)
		p.handleAgentEvent(agent.CompactionEndEvent{Reason: "manual", Aborted: true})
		if p.indicatorKind() != "retry" {
			t.Fatalf("compaction_end cleared the %q status", p.indicatorKind())
		}
	})
	t.Run("queued messages", func(t *testing.T) {
		p := newEventProbe(t, false)
		start(p)
		p.compactionQueue = []compactionQueuedMessage{{text: "queued"}}
		p.handleAgentEvent(agent.CompactionEndEvent{Reason: "manual", Aborted: true, WillRetry: true})
		if len(p.compactionQueue) != 0 {
			t.Fatalf("compaction_end left %d queued messages", len(p.compactionQueue))
		}
	})
}

// auto_retry_start shows the retry status; auto_retry_end clears only that status and reports a failure with its attempt count and error, or
// "Unknown error" when none is given, and nothing on success.
func TestEventAutoRetry(t *testing.T) {
	p := newEventProbe(t, false)
	p.handleAgentEvent(agent.AutoRetryStartEvent{Attempt: 1, MaxAttempts: 3, DelayMs: 2500})
	if p.indicatorKind() != "retry" || !strings.Contains(p.indicatorText(), "Retrying (1/3) in 3s") {
		t.Fatalf("status %q", p.indicatorKind())
	}
	before := p.chat()
	p.handleAgentEvent(agent.AutoRetryEndEvent{Success: true, Attempt: 1})
	if p.indicatorKind() != "" || p.chat() != before {
		t.Fatalf("success: status=%q chat=%q", p.indicatorKind(), p.chat())
	}
	p.handleAgentEvent(agent.AutoRetryEndEvent{Success: false, Attempt: 3, FinalError: "overloaded"})
	p.handleAgentEvent(agent.AutoRetryEndEvent{Success: false, Attempt: 2})
	got := p.chat()
	if !strings.Contains(got, "Error: Retry failed after 3 attempts: overloaded") || !strings.Contains(got, "Error: Retry failed after 2 attempts: Unknown error") {
		t.Fatalf("chat = %q", got)
	}
	p.showCompactionStatusIndicator("manual")
	p.handleAgentEvent(agent.AutoRetryEndEvent{Success: true})
	if p.indicatorKind() != "compaction" {
		t.Fatalf("auto_retry_end cleared the %q status", p.indicatorKind())
	}
}

// A scheduled summarization retry shows the error and the retry status; its attempt start swaps the retry status for the branch-summary status or
// the compaction status of its reason; finishing clears the retry status.
func TestEventSummarizationRetry(t *testing.T) {
	p := newEventProbe(t, false)
	p.handleAgentEvent(agent.SummarizationRetryScheduledEvent{Attempt: 1, MaxAttempts: 4, DelayMs: 1000, ErrorMessage: "RATE LIMITED"})
	if p.indicatorKind() != "retry" || !strings.Contains(p.chat(), "Error: RATE LIMITED") {
		t.Fatalf("status=%q chat=%q", p.indicatorKind(), p.chat())
	}
	p.handleAgentEvent(agent.SummarizationRetryAttemptStartEvent{Source: "branchSummary"})
	if p.indicatorKind() != "branchSummary" {
		t.Fatalf("branch summary attempt: status %q", p.indicatorKind())
	}
	p.handleAgentEvent(agent.SummarizationRetryScheduledEvent{Attempt: 2, MaxAttempts: 4, DelayMs: 1000, ErrorMessage: "AGAIN"})
	p.handleAgentEvent(agent.SummarizationRetryAttemptStartEvent{Source: "compaction", Reason: "overflow"})
	if p.indicatorKind() != "compaction" || !strings.Contains(p.indicatorText(), "Context overflow detected") {
		t.Fatalf("compaction attempt: status %q", p.indicatorKind())
	}
	p.handleAgentEvent(agent.SummarizationRetryScheduledEvent{Attempt: 3, MaxAttempts: 4, DelayMs: 1000, ErrorMessage: "THIRD"})
	p.handleAgentEvent(agent.SummarizationRetryFinishedEvent{})
	if p.indicatorKind() != "" {
		t.Fatalf("finished left the %q status", p.indicatorKind())
	}
	p.showCompactionStatusIndicator("manual")
	p.handleAgentEvent(agent.SummarizationRetryFinishedEvent{})
	if p.indicatorKind() != "compaction" {
		t.Fatalf("finished cleared the %q status", p.indicatorKind())
	}
}

// message_end requests a frame for an assistant message, so its final content is painted; a user message_end requests none.
func TestEventMessageEndRequestsAFrame(t *testing.T) {
	for _, tc := range []struct {
		name  string
		end   func(msg agent.AgentMessage) agent.AgentMessage
		final bool
	}{
		{"assistant", func(msg agent.AgentMessage) agent.AgentMessage { return msg }, true},
		{"user", func(agent.AgentMessage) agent.AgentMessage { return userMsg("done") }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, term := newTickRenderProbe(t, "regular")
				settle := func() string {
					time.Sleep(100 * time.Millisecond)
					synctest.Wait()
					m.drainMainLoopOnce()
					return widthx.StripAnsi(term.take())
				}
				settle()
				msg := assistantMsg("", ai.TextContent{Text: "DRAFT"})
				m.handleAgentEvent(agent.MessageStartEvent{Message: msg})
				settle()
				final := assistantMsg("", ai.TextContent{Text: "FINAL"})
				m.handleAgentEvent(agent.MessageEndEvent{Message: tc.end(final)})
				frame := settle()
				if got := strings.Contains(frame, "FINAL"); got != tc.final {
					t.Fatalf("frame after message_end shows FINAL = %v, want %v: %q", got, tc.final, frame)
				}
			})
		})
	}
}

// A tool still running when the run ends is settled, and the pending tool arguments are forgotten.
func TestEventAgentEndSettlesRunningToolsAndForgetsPendingArguments(t *testing.T) {
	p := newEventProbe(t, false)
	startReadTool(p, "a")
	card := p.toolByID["a"]
	p.handleAgentEvent(agent.MessageStartEvent{Message: assistantMsg("")})
	p.handleAgentEvent(agent.MessageUpdateEvent{Message: assistantMsg(""), AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 0, Partial: toolCallPartial("b", "read")}})
	if len(p.pendingArgs) == 0 {
		t.Fatal("precondition: pending arguments")
	}
	p.handleAgentEvent(agent.AgentEndEvent{})
	if card.State == tui.ToolStateRunning || len(p.pendingArgs) != 0 || len(p.toolStarts) != 0 {
		t.Fatalf("state=%v pendingArgs=%d toolStarts=%d, want the tool settled and the arguments forgotten", card.State, len(p.pendingArgs), len(p.toolStarts))
	}
}

// A user message_start refreshes the pending-messages display, so a queued message that the run consumed leaves it.
func TestEventUserMessageStartRefreshesThePendingMessages(t *testing.T) {
	p := newEventProbe(t, false)
	p.steerMessageWithImages("QUEUED STEER", nil)
	pending := func() string { return widthx.StripAnsi(strings.Join(p.pendingMessagesContainer.Render(100), "\n")) }
	if !strings.Contains(pending(), "QUEUED STEER") {
		t.Fatalf("precondition: pending %q", pending())
	}
	p.agent.ClearAllQueues()
	p.handleAgentEvent(agent.MessageStartEvent{Message: userMsg("QUEUED STEER")})
	if strings.Contains(pending(), "QUEUED STEER") {
		t.Fatalf("a consumed queued message stays pending: %q", pending())
	}
}

// A tool call a streamed message created fills in its arguments as they stream; the start event supplies the final ones. The start marks the
// execution started on a streamed card and on a fresh one, and a block of the message the call belongs to loses its zone marks.
func TestEventToolCardArgumentsAndExecutionStart(t *testing.T) {
	p := newEventProbe(t, false)
	msg := assistantMsg("", ai.TextContent{Text: "calling"})
	p.handleAgentEvent(agent.MessageStartEvent{Message: msg})
	call := ai.ToolCall{ID: "a", Name: "read", Arguments: map[string]any{"path": "streamed.txt"}}
	msg.Assistant.Content = []ai.AssistantContentBlock{ai.TextContent{Text: "calling"}, call}
	p.handleAgentEvent(agent.MessageUpdateEvent{Message: msg, AssistantMessageEvent: ai.ToolCallDeltaEvent{ContentIndex: 1, Delta: `{"path":"streamed.txt"}`, Partial: &ai.AssistantMessage{Content: msg.Assistant.Content}}})
	if got := p.chat(); !strings.Contains(got, "read streamed.txt") {
		t.Fatalf("the streamed arguments are not drawn: %q", got)
	}
	card := p.toolByID["a"]
	if !card.StartedAt.IsZero() {
		t.Fatal("a streamed call is not started before its start event")
	}
	p.evCurrentBlock.SetHasToolCalls(false)
	p.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "a", ToolName: "read", Args: json.RawMessage(`{"path":"final.txt"}`)})
	if got := p.chat(); !strings.Contains(got, "read final.txt") || strings.Contains(got, "streamed.txt") {
		t.Fatalf("the start event's final arguments are not drawn: %q", got)
	}
	if card.StartedAt.IsZero() || len(p.toolByID) != 1 {
		t.Fatalf("streamed card started=%v, cards=%d", !card.StartedAt.IsZero(), len(p.toolByID))
	}
	if strings.Contains(strings.Join(p.evCurrentBlock.Render(80), ""), "\x1b]133;A") {
		t.Fatal("a block whose message runs a tool is not zoned")
	}
	startReadTool(p, "fresh")
	if p.toolByID["fresh"].StartedAt.IsZero() {
		t.Fatal("a fresh card is not started")
	}
}

// A partial result is shown on a card that keeps running and is never an error; the end carries the recorded duration.
func TestEventToolUpdateIsPartialAndEndRecordsTheDuration(t *testing.T) {
	p := newEventProbe(t, false)
	startReadTool(p, "a")
	card := p.toolByID["a"]
	p.handleAgentEvent(agent.ToolExecutionUpdateEvent{ToolCallID: "a", ToolName: "read", PartialResult: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "PARTIAL"}}, IsError: true}})
	if card.State != tui.ToolStateRunning || card.Output != "PARTIAL" {
		t.Fatalf("state=%v output=%q, want a running card showing the partial result", card.State, card.Output)
	}
	duration := int64(1234)
	p.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "a", ToolName: "read", DurationMs: &duration, Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "FINAL"}}}})
	if card.State != tui.ToolStateDone || card.DurationMs == nil || *card.DurationMs != duration {
		t.Fatalf("state=%v duration=%v, want a settled card with its recorded duration", card.State, card.DurationMs)
	}
}

// Each event that changes the transcript is painted: a streamed delta, the end of the run, a tool result and a retry status.
func TestEventsRequestFrames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, term := newTickRenderProbe(t, "regular")
		settle := func() string {
			time.Sleep(100 * time.Millisecond)
			synctest.Wait()
			m.drainMainLoopOnce()
			return widthx.StripAnsi(term.take())
		}
		settle()
		msg := assistantMsg("", ai.TextContent{Text: "DRAFT"})
		m.handleAgentEvent(agent.MessageStartEvent{Message: msg})
		settle()
		msg.Assistant.Content = []ai.AssistantContentBlock{ai.TextContent{Text: "UPDATED"}}
		m.handleAgentEvent(agent.MessageUpdateEvent{Message: msg, AssistantMessageEvent: ai.TextDeltaEvent{ContentIndex: 0, Delta: "UPDATED"}})
		if frame := settle(); !strings.Contains(frame, "UPDATED") {
			t.Fatalf("message_update painted no frame: %q", frame)
		}
		m.handleAgentEvent(agent.AgentEndEvent{})
		if frame := settle(); strings.Contains(frame, "UPDATED") || frame == "" {
			t.Fatalf("agent_end painted %q, want a frame without the removed message", frame)
		}
	})
}
