package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts handleEvent (3467-3865): session_info_changed and the message events
// that arrive with no streaming message.

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// session_info_changed sets the terminal title from the session name and the working directory.
func TestHandleEventSessionInfoChangedSetsTheTerminalTitle(t *testing.T) {
	var titles []string
	previous := setTerminalTitle
	setTerminalTitle = func(title string) { titles = append(titles, title) }
	t.Cleanup(func() { setTerminalTitle = previous })
	p := newEventProbe(t, false)
	p.opts.CWD = "/work/project"
	p.handleAgentEvent(agent.SessionInfoChangedEvent{Name: "named"})
	p.handleAgentEvent(agent.SessionInfoChangedEvent{})
	if want := []string{tui.BuildTerminalTitle("named", "/work/project"), tui.BuildTerminalTitle("", "/work/project")}; !slices.Equal(titles, want) {
		t.Fatalf("titles = %q, want %q", titles, want)
	}
}

// message_update changes nothing without a streaming message: it creates no tool card.
func TestHandleEventMessageUpdateNeedsAStreamingMessage(t *testing.T) {
	p := newEventProbe(t, false)
	p.handleAgentEvent(agent.MessageUpdateEvent{Message: assistantMsg("", ai.ToolCall{ID: "call-1", Name: "read"}), AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 0, Partial: toolCallPartial("call-1", "read")}})
	if len(p.toolByID) != 0 || len(p.pendingArgs) != 0 || p.chat() != "" {
		t.Fatalf("toolByID=%d pendingArgs=%d chat=%q, want nothing drawn", len(p.toolByID), len(p.pendingArgs), p.chat())
	}
}

// An assistant message_end without a streaming message leaves the pending tools alone.
func TestHandleEventMessageEndNeedsAStreamingMessage(t *testing.T) {
	p := newEventProbe(t, false)
	startReadTool(p, "a")
	card := p.toolByID["a"]
	end := assistantMsg("", ai.ToolCall{ID: "a", Name: "read"})
	end.Assistant.StopReason = ai.StopReasonAborted
	p.handleAgentEvent(agent.MessageEndEvent{Message: end})
	if card.State != tui.ToolStateRunning || len(p.toolByID) != 1 {
		t.Fatalf("state=%v cards=%d, want the running card untouched", card.State, len(p.toolByID))
	}
}

func missingCacheMessage(stop ai.StopReason, errorMessage string) agent.AgentMessage {
	message := assistantMsg("", ai.TextContent{Text: "reply"})
	message.Assistant.StopReason, message.Assistant.ErrorMessage = stop, errorMessage
	message.Assistant.Provider, message.Assistant.ModelID = "p", "m"
	message.Assistant.Usage = &ai.Usage{Input: 200000, Cost: ai.UsageCost{Input: 1}}
	message.Assistant.Diagnostics = []ai.AssistantMessageDiagnostic{{Type: "anthropic_input_transformations", Details: map[string]any{"transformations": []any{map[string]any{"type": "thinking_dropped"}}}}}
	return message
}

// message_end shows the thinking-drop notice and then the cache-miss notice for a response that finished; an aborted or failed response shows
// neither.
func TestHandleEventMessageEndShowsTheNoticesOfAFinishedResponseOnly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stop    ai.StopReason
		message string
		notices bool
	}{
		{"finished", ai.StopReasonStop, "", true},
		{"failed", ai.StopReasonError, "invalid request", false},
		{"aborted", ai.StopReasonAborted, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newEventProbe(t, false)
			dir := t.TempDir()
			sess, err := NewSessionManagerWithDir(dir, dir).Create("notices", "")
			if err != nil {
				t.Fatal(err)
			}
			sess.stats.cachePrev = &previousCacheRequest{promptTokens: 200000, modelKey: "p/m", reportedCache: true}
			p.opts.SessionHandle = &recordingCompactHandle{inner: sess}
			p.opts.Settings = Settings{ShowCacheMissNotices: true}
			p.handleAgentEvent(agent.MessageStartEvent{Message: assistantMsg("", ai.TextContent{Text: "reply"})})
			p.handleAgentEvent(agent.MessageEndEvent{Message: missingCacheMessage(tc.stop, tc.message)})
			got := p.chat()
			dropped, miss := strings.Index(got, "Anthropic dropped 1 thinking block"), strings.Index(got, "Cache miss")
			if tc.notices && (dropped < 0 || miss <= dropped) {
				t.Fatalf("chat = %q, want the thinking-drop notice and then the cache-miss notice", got)
			}
			if !tc.notices && (dropped >= 0 || miss >= 0) {
				t.Fatalf("chat = %q, want no notice", got)
			}
		})
	}
}
