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

func TestWorkingStatusUsesEditorBorder(t *testing.T) {
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir()})
	m.editor = tui.NewEditor()
	m.editor.EmbedWorkingStatus = true
	m.statusContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 80, 24)
	m.handleAgentEvent(agent.AgentStartEvent{})
	if lines := m.statusContainer.Render(80); len(lines) != 0 {
		t.Fatalf("status occupies separate rows: %#v", lines)
	}
	top := widthx.StripAnsi(strings.Join(m.editor.Render(80), "\n"))
	if !strings.Contains(top, "── ⠋ Working ─") || strings.Contains(top, "Working...") {
		t.Fatalf("editor status=%q", top)
	}
	m.handleAgentEvent(agent.AgentEndEvent{})
	if strings.Contains(strings.Join(m.editor.Render(80), "\n"), "Working") {
		t.Fatal("settled status remains")
	}
}

func TestWorkingStatusStaysVisibleThroughPendingAndRunningToolsUntilAgentEnd(t *testing.T) {
	m := statusBorderMode(t, true)
	assistant := agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role:       agent.RoleAssistant,
		Content:    []ai.AssistantContentBlock{ai.ToolCall{ID: "call-1", Name: "read", Arguments: ai.JsonObject{"path": "main.go"}}},
		StopReason: ai.StopReasonStop,
	}}
	events := []agent.AgentEvent{
		agent.AgentStartEvent{},
		agent.MessageStartEvent{Message: assistant},
		agent.MessageEndEvent{Message: assistant},
		agent.ToolExecutionStartEvent{ToolCallID: "call-1", ToolName: "read", Args: json.RawMessage(`{"path":"main.go"}`)},
		agent.ToolExecutionEndEvent{ToolCallID: "call-1", ToolName: "read", Result: agent.AgentToolResult{Content: "contents"}},
		agent.TurnEndEvent{Message: assistant},
	}
	for _, event := range events {
		m.handleAgentEvent(event)
		if m.activeStatusIndicator == nil || m.activeStatusIndicator.Kind != "working" {
			t.Fatalf("%T cleared the working indicator while tool work was pending or running", event)
		}
	}

	m.handleAgentEvent(agent.AgentEndEvent{})
	if m.activeStatusIndicator != nil {
		t.Fatalf("agent_end retained status indicator %#v", m.activeStatusIndicator)
	}
}

func statusBorderMode(t *testing.T, embedded bool) *InteractiveMode {
	t.Helper()
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir()})
	m.editor = tui.NewEditor()
	m.editor.EmbedWorkingStatus = embedded
	m.statusContainer = tui.NewContainer()
	m.chatContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 80, 24)
	m.statusLine = NewStatusLine(nil, "", nil)
	m.agent = agent.NewAgent(agent.AgentOptions{})
	return m
}

func TestStatusBorderIdleCompactionAndMatchingEnd(t *testing.T) {
	m := statusBorderMode(t, true)
	m.isIdle = true
	m.handleAgentEvent(agent.CompactionStartEvent{Reason: "manual"})
	if got := widthx.StripAnsi(m.editor.Render(80)[1]); !strings.Contains(got, "── ⠋ Compacting context... (escape to cancel)") {
		t.Fatalf("compaction border=%q", got)
	}
	if len(m.statusContainer.Render(80)) != 0 {
		t.Fatal("embedded compaction reserves status rows")
	}
	m.tickStatusIndicators(m.statusLastFrame.Add(80 * time.Millisecond))
	if m.activeStatusIndicator.Frame != 1 {
		t.Fatal("idle manual compaction does not animate")
	}
	m.stopWorkingLoader()
	if m.activeStatusIndicator == nil || m.activeStatusIndicator.Kind != "compaction" {
		t.Fatal("working end cleared compaction")
	}
	m.startWorkingLoader()
	m.handleAgentEvent(agent.CompactionEndEvent{Reason: "manual"})
	if m.activeStatusIndicator == nil || m.activeStatusIndicator.Kind != "working" {
		t.Fatal("compaction end cleared replacement working status")
	}
	m.stopWorkingLoader()
	if strings.Contains(widthx.StripAnsi(m.editor.Render(80)[1]), "Working") {
		t.Fatal("working end retained status")
	}
}

func TestStatusBorderFallbackAndIdleRows(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		for _, embedded := range []bool{false, true} {
			m := statusBorderMode(t, embedded)
			m.opts.Settings.TuiMode = mode
			m.opts.Settings.ClearOnShrink = new(true)
			m.tuiInst.SetClearOnShrink(true)
			m.startWorkingLoader()
			if standalone := len(m.statusContainer.Render(40)); (standalone == 0) != embedded {
				t.Fatalf("mode=%s embedded=%v status rows=%d", mode, embedded, standalone)
			}
			m.stopWorkingLoader()
			want := 0
			if !embedded && mode == "regular" {
				want = 2
			}
			if got := len(m.statusContainer.Render(40)); got != want {
				t.Fatalf("mode=%s embedded=%v idle rows=%d want%d", mode, embedded, got, want)
			}
		}
	}
}

func TestWorkingStatusOptionsAndVisibility(t *testing.T) {
	m := statusBorderMode(t, true)
	m.isIdle = false
	m.startWorkingLoader()
	ui := &ExtUIContext{m: m}
	ui.SetWorkingMessage("Indexing")
	ui.SetWorkingIndicator(map[string]any{"frames": []string{"A", "B"}, "intervalMs": 200})
	if got := widthx.StripAnsi(m.editor.Render(80)[1]); !strings.Contains(got, "── A Indexing ") {
		t.Fatalf("custom status=%q", got)
	}
	m.tickStatusIndicators(m.statusLastFrame.Add(100 * time.Millisecond))
	if m.activeStatusIndicator.Frame != 0 {
		t.Fatal("custom interval advanced early")
	}
	m.tickStatusIndicators(m.statusLastFrame.Add(200 * time.Millisecond))
	if m.activeStatusIndicator.Frame != 1 {
		t.Fatal("custom interval did not advance")
	}
	ui.SetWorkingIndicator(map[string]any{"frames": []string{}})
	if got := widthx.StripAnsi(m.editor.Render(80)[1]); !strings.Contains(got, "── Indexing ") {
		t.Fatalf("hidden spinner=%q", got)
	}
	ui.SetWorkingVisible(false)
	if m.activeStatusIndicator != nil {
		t.Fatal("working visibility did not clear indicator")
	}
	ui.SetWorkingVisible(true)
	if m.activeStatusIndicator == nil {
		t.Fatal("working visibility did not restore indicator")
	}
	ui.SetWorkingIndicator(nil)
	ui.SetWorkingMessage("")
	if got := widthx.StripAnsi(m.editor.Render(80)[1]); !strings.Contains(got, "── ⠋ Working ") {
		t.Fatalf("reset status=%q", got)
	}
	m.handleAgentEvent(agent.CompactionStartEvent{Reason: "threshold"})
	ui.SetWorkingVisible(false)
	if m.activeStatusIndicator.Kind != "compaction" {
		t.Fatal("working visibility cleared compaction")
	}
}

func TestStatusReplacementDisposesQueuedRetryFrames(t *testing.T) {
	m := statusBorderMode(t, true)
	m.setStatusContainerLabel("Retrying")
	stop := make(chan struct{})
	m.retryCountdownStop = func() { close(stop) }
	m.postRetryStatusUpdate(stop, "stale retry")
	m.handleAgentEvent(agent.CompactionStartEvent{Reason: "manual"})
	select {
	case <-stop:
	default:
		t.Fatal("replaced retry timer not disposed")
	}
	select {
	case apply := <-m.uiTaskCh:
		apply()
	default:
		t.Fatal("retry frame was not queued")
	}
	if m.activeStatusIndicator.Kind != "compaction" || strings.Contains(m.activeStatusIndicator.Message, "stale") {
		t.Fatal("stale retry overwrote replacement")
	}
}

func TestStatusTickStopsWithOwnerContext(t *testing.T) {
	m := statusBorderMode(t, true)
	m.startWorkingLoader()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); m.tickSpinner(ctx) }()
	var apply func()
	select {
	case apply = <-m.uiTaskCh:
	case <-time.After(5 * time.Second):
		t.Fatal("ticker did not queue frame")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ticker did not stop")
	}
	apply()
	if m.activeStatusIndicator.Frame != 0 {
		t.Fatal("cancelled owner's queued tick mutated status")
	}
}

func TestWorkingOptionsAreOwnedAndSurviveCallerMutation(t *testing.T) {
	m := statusBorderMode(t, true)
	m.startWorkingLoader()
	m.runCtx = t.Context()
	ui := &ExtUIContext{m: m}
	frames := []string{"source"}
	ui.SetWorkingIndicator(map[string]any{"frames": frames})
	frames[0] = "mutated"
	if m.activeStatusIndicator.Frames[0] != "⠋" {
		t.Fatal("extension mutated UI off owner loop")
	}
	select {
	case apply := <-m.uiTaskCh:
		apply()
	default:
		t.Fatal("owner update missing")
	}
	if m.activeStatusIndicator.Frames[0] != "source" {
		t.Fatal("queued options alias caller")
	}
}

func TestRetryStatusRemainsAtZeroUntilEnd(t *testing.T) {
	m := statusBorderMode(t, true)
	m.handleAgentEvent(agent.AutoRetryStartEvent{Attempt: 1, MaxAttempts: 2, DelayMs: 100})
	defer m.clearStatusIndicator("")
	if m.activeStatusIndicator == nil {
		t.Fatal("short retry has no status")
	}
	select {
	case apply := <-m.uiTaskCh:
		apply()
	case <-time.After(5 * time.Second):
		t.Fatal("countdown did not reach zero")
	}
	if m.activeStatusIndicator == nil || !strings.Contains(m.activeStatusIndicator.Message, "in 0s") {
		t.Fatalf("expired retry status = %#v", m.activeStatusIndicator)
	}
	m.handleAgentEvent(agent.AutoRetryEndEvent{Success: true})
	if m.activeStatusIndicator != nil {
		t.Fatal("retry end retained indicator")
	}
}

// Upstream auto_retry_end shows every final failure, a cancelled retry
// included, with showError("Retry failed after N attempts: ..."), so it stays
// in the transcript instead of flashing in the footer.
func TestAutoRetryEndFailureShowsPersistentError(t *testing.T) {
	for _, tc := range []struct {
		event agent.AutoRetryEndEvent
		want  string
	}{
		{agent.AutoRetryEndEvent{Attempt: 3, FinalError: "overloaded_error"}, "Error: Retry failed after 3 attempts: overloaded_error"},
		{agent.AutoRetryEndEvent{Attempt: 1, FinalError: "Retry cancelled"}, "Error: Retry failed after 1 attempts: Retry cancelled"},
		{agent.AutoRetryEndEvent{Attempt: 2}, "Error: Retry failed after 2 attempts: Unknown error"},
	} {
		m := statusBorderMode(t, true)
		m.handleAgentEvent(tc.event)
		chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(80), "\n"))
		if !strings.Contains(chat, tc.want) {
			t.Fatalf("chat after %+v = %q, want %q", tc.event, chat, tc.want)
		}
	}
	m := statusBorderMode(t, true)
	m.handleAgentEvent(agent.AutoRetryEndEvent{Success: true, Attempt: 1})
	if chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(80), "\n")); strings.Contains(chat, "Retry failed") {
		t.Fatalf("a successful retry shows an error: %q", chat)
	}
}

// Upstream summarization_retry_scheduled shows the error with showError, a
// transcript entry, not a footer flash with a fixed lifetime (GUARD-10).
func TestSummarizationRetryScheduledShowsTheError(t *testing.T) {
	m := statusBorderMode(t, true)
	m.handleAgentEvent(agent.SummarizationRetryScheduledEvent{ErrorMessage: "summarizer overloaded", Attempt: 1, MaxAttempts: 3})
	chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(80), "\n"))
	if !strings.Contains(chat, "Error: summarizer overloaded") {
		t.Fatalf("chat = %q, want the summarization error shown", chat)
	}
}
