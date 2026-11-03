package codingagent

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// Boundary obligations of renderSessionEntries and renderSessionItems (packages/coding-agent/src/modes/interactive/interactive-mode.ts):
// one transcript item per entry in order, a tool card per tool call settled by the stop reason or its tool result, cache-miss and
// cost notices that depend on the stop reason and the settings, the history that populateHistory fills, and the entries that render
// nothing. Every case rebuilds the transcript from a persisted Session, as /resume, /tree and a replacement do.

func contractSession(t *testing.T, messages ...agent.AgentMessage) *Session {
	t.Helper()
	session := NewSession("render-items-contract", t.TempDir())
	for _, message := range messages {
		if _, err := session.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	return session
}

func contractMode(t *testing.T, session *Session, notices bool) *InteractiveMode {
	t.Helper()
	m := cacheWarmingTestMode(t, session, notices)
	m.toolByID = make(map[string]*tui.ToolExecutionComponent)
	m.toolStarts = make(map[string]time.Time)
	m.outputPad = 1
	return m
}

func contractToolCall(id string) ai.ToolCall {
	return ai.ToolCall{ID: id, Name: "read", Arguments: ai.JsonObject{"path": "file.go"}}
}

func contractChatText(m *InteractiveMode) string {
	return stripANSITest(strings.Join(m.chatContainer.Render(100), "\n"))
}

func TestRenderSessionItemsSettlesEachToolCardByTheStopReason(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reason  ai.StopReason
		errText string
		retries int
		want    string
		state   tui.ToolExecutionState
	}{
		{"aborted", ai.StopReasonAborted, "", 0, "Operation aborted", tui.ToolStateError},
		{"aborted after one retry", ai.StopReasonAborted, "", 1, "Aborted after 1 retry attempt", tui.ToolStateError},
		{"aborted after retries", ai.StopReasonAborted, "", 3, "Aborted after 3 retry attempts", tui.ToolStateError},
		{"error with a message", ai.StopReasonError, "provider exploded", 0, "provider exploded", tui.ToolStateError},
		{"error without a message", ai.StopReasonError, "", 0, "Error", tui.ToolStateError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assistant := assistantMsg("", contractToolCall("call-1"), contractToolCall("call-2"))
			assistant.Assistant.StopReason = tc.reason
			assistant.Assistant.ErrorMessage = tc.errText
			session := contractSession(t, userMsg("question"), assistant)
			m := contractMode(t, session, false)
			m.opts.SessionHandle.(*recordingCompactHandle).retryAttempt = tc.retries
			m.renderSessionEntries()
			if len(m.toolOrder) != 2 {
				t.Fatalf("rendered %d tool cards, want one per tool call", len(m.toolOrder))
			}
			for i, card := range m.toolOrder {
				if card.State != tc.state {
					t.Errorf("card %d state = %v, want the error state", i, card.State)
				}
			}
			if got := strings.Count(contractChatText(m), tc.want); got != 2 {
				t.Errorf("transcript names %q %d times, want once per card:\n%s", tc.want, got, contractChatText(m))
			}
		})
	}
}

func TestRenderSessionItemsLeavesAnAnsweredCallPendingUntilItsResult(t *testing.T) {
	assistant := assistantMsg("", contractToolCall("pending-call"), contractToolCall("answered-call"))
	failed := agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "answered-call", ToolName: "read", IsError: true, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "no such file"}}}}
	m := contractMode(t, contractSession(t, userMsg("question"), assistant, failed), false)
	m.renderSessionEntries()
	if len(m.toolOrder) != 2 {
		t.Fatalf("rendered %d tool cards, want 2", len(m.toolOrder))
	}
	if m.toolOrder[0].State == tui.ToolStateError || m.toolOrder[0].State == tui.ToolStateDone {
		t.Errorf("a call without a result settled as state %v", m.toolOrder[0].State)
	}
	if _, pending := m.toolByID["pending-call"]; !pending {
		t.Error("the call without a result is not tracked as pending")
	}
	if _, pending := m.toolByID["answered-call"]; pending {
		t.Error("the answered call is still tracked as pending")
	}
	if m.toolOrder[1].State != tui.ToolStateError {
		t.Errorf("an error result left the card in state %v, want the error state", m.toolOrder[1].State)
	}
	if got := contractChatText(m); !strings.Contains(got, "no such file") {
		t.Errorf("the result text is not on its card:\n%s", got)
	}
}

func TestRenderSessionItemsExpandsCardsLikeTheToolToggle(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		m := contractMode(t, contractSession(t, userMsg("question"), assistantMsg("", contractToolCall("call-1"))), false)
		m.toolsExpanded = expanded
		m.renderSessionEntries()
		if len(m.toolOrder) != 1 || m.toolOrder[0].Collapsed == expanded {
			t.Fatalf("tools expanded %v: card collapsed = %v", expanded, m.toolOrder[0].Collapsed)
		}
	}
}

func TestRenderSessionItemsCacheMissNoticesFollowTheStopReasonAndTheSetting(t *testing.T) {
	build := func(second ai.StopReason, notices bool) *InteractiveMode {
		cached := &agent.AssistantMessage{Role: "assistant", Provider: "test", ModelID: "model", Timestamp: 1, StopReason: ai.StopReasonStop, Usage: &ai.Usage{Input: 1_000, CacheWrite: 49_000}}
		uncached := &agent.AssistantMessage{Role: "assistant", Provider: "test", ModelID: "model", Timestamp: 2, StopReason: second, Usage: &ai.Usage{Input: 50_000}}
		session := contractSession(t, agent.AgentMessage{Assistant: cached}, agent.AgentMessage{Assistant: uncached})
		m := contractMode(t, session, notices)
		m.renderSessionEntries()
		return m
	}
	const notice = "Cache miss: 50k tokens re-billed"
	if got := contractChatText(build(ai.StopReasonStop, true)); !strings.Contains(got, notice) {
		t.Fatalf("a completed response has no notice:\n%s", got)
	}
	for name, m := range map[string]*InteractiveMode{
		"aborted response":  build(ai.StopReasonAborted, true),
		"error response":    build(ai.StopReasonError, true),
		"notices turned off": build(ai.StopReasonStop, false),
	} {
		if got := contractChatText(m); strings.Contains(got, notice) {
			t.Errorf("%s still shows the notice:\n%s", name, got)
		}
	}
}

func TestRenderSessionItemsCompactionAndBranchSummaryCostNotices(t *testing.T) {
	usage := &ai.Usage{Input: 3_000, Output: 500, Cost: ai.UsageCost{Total: 0.25}}
	compacted := contractSession(t, userMsg("question"))
	if _, err := compacted.AppendCompaction("compacted summary", "", 1000, nil, false, usage); err != nil {
		t.Fatal(err)
	}
	branched := contractSession(t, userMsg("question"))
	if _, err := branched.BranchWithSummary(branched.GetLeafID(), "branch summary text", nil, false, usage); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		session *Session
		want    string
	}{
		{compacted, "Compaction: 3.5k tokens billed (~$0.25)"},
		{branched, "Branch summary: 3.5k tokens billed (~$0.25)"},
	} {
		for _, notices := range []bool{true, false} {
			m := contractMode(t, tc.session, notices)
			m.renderSessionEntries()
			if got := contractChatText(m); strings.Contains(got, tc.want) != notices {
				t.Errorf("notices %v: transcript contains %q = %v:\n%s", notices, tc.want, strings.Contains(got, tc.want), got)
			}
		}
	}
}

func TestRenderSessionItemsDropsAnEmptyBranchSummaryAndItsCostNotice(t *testing.T) {
	session := contractSession(t, userMsg("question"))
	if _, err := session.BranchWithSummary(session.GetLeafID(), "", nil, false, &ai.Usage{Input: 3_000, Cost: ai.UsageCost{Total: 0.25}}); err != nil {
		t.Fatal(err)
	}
	m := contractMode(t, session, true)
	m.renderSessionEntries()
	if got := contractChatText(m); strings.Contains(got, "Branch summary") {
		t.Fatalf("an empty branch summary rendered:\n%s", got)
	}
}

func TestRenderSessionItemsRendersCustomEntriesOnlyWithARegisteredRenderer(t *testing.T) {
	session := contractSession(t, userMsg("question"))
	if _, err := session.AppendCustomEntry("note", "entry-data"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendCustomMessageEntry("hidden", "hidden-content", false, nil); err != nil {
		t.Fatal(err)
	}
	ext := extension.Extension{EntryRenderers: map[string]extension.EntryRenderer{
		"note": func(entry extension.CustomEntry, _ extension.EntryRenderOptions, _ extension.Theme) extension.Component {
			return tui.NewText("entry:" + entry.CustomType)
		},
	}}
	with := contractMode(t, session, false)
	with.newRunner = inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	with.renderSessionEntries()
	got := contractChatText(with)
	if !strings.Contains(got, "entry:note") {
		t.Errorf("the custom entry did not render through its renderer:\n%s", got)
	}
	if strings.Contains(got, "hidden-content") {
		t.Errorf("a custom message with display off rendered:\n%s", got)
	}
	without := contractMode(t, session, false)
	without.renderSessionEntries()
	if got := contractChatText(without); strings.Contains(got, "entry:note") || strings.Contains(got, "entry-data") {
		t.Errorf("a custom entry rendered without a renderer:\n%s", got)
	}
}

func TestRenderSessionItemsSkipsAUserMessageWithoutText(t *testing.T) {
	empty := agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: ""}}}}
	m := contractMode(t, contractSession(t, empty, userMsg("visible prompt")), false)
	m.renderSessionEntries()
	if len(m.userBlocks) != 1 {
		t.Fatalf("rendered %d user blocks, want only the message with text", len(m.userBlocks))
	}
}

// populateHistory fills the editor history with the rendered user messages, oldest first; renderSessionEntries alone leaves it empty.
func TestRenderSessionEntriesPopulatesTheEditorHistoryOnlyWhenAsked(t *testing.T) {
	session := contractSession(t, userMsg("first prompt"), userMsg("second prompt"))
	recall := func(m *InteractiveMode, presses int) string {
		for range presses {
			m.editor.HandleInput("\x1b[A")
		}
		return m.editor.Text()
	}
	with := contractMode(t, session, false)
	with.editor = tui.NewEditor()
	with.renderSessionEntriesWithHistory(true)
	if got := recall(with, 1); got != "second prompt" {
		t.Errorf("first Up recalled %q, want the newest prompt", got)
	}
	if got := recall(with, 1); got != "first prompt" {
		t.Errorf("second Up recalled %q, want the older prompt", got)
	}
	without := contractMode(t, session, false)
	without.editor = tui.NewEditor()
	without.renderSessionEntries()
	if got := recall(without, 2); got != "" {
		t.Errorf("renderSessionEntries filled the history: Up recalled %q", got)
	}
	// renderCurrentSessionState repaints through renderInitialMessages, which populates the history.
	replaced := contractMode(t, session, false)
	replaced.editor = tui.NewEditor()
	replaced.renderCurrentSessionState()
	if got := recall(replaced, 1); got != "second prompt" {
		t.Errorf("renderCurrentSessionState left the history unpopulated: Up recalled %q", got)
	}
}

func TestRenderCurrentSessionStateShowsTheCompactionCount(t *testing.T) {
	session := contractSession(t, userMsg("question"))
	for range 2 {
		if _, err := session.AppendCompaction("summary", "", 1000, nil, false, nil); err != nil {
			t.Fatal(err)
		}
	}
	m := contractMode(t, session, false)
	m.renderCurrentSessionState()
	if got := contractChatText(m); !strings.Contains(got, "Session compacted 2 times") {
		t.Fatalf("the repaint did not report the compactions:\n%s", got)
	}
}

// Rendering the entries refreshes the footer with the current Session's name, so a replacement shows the new Session's name, or none.
func TestRenderSessionEntriesRefreshesTheFooterSessionName(t *testing.T) {
	named := contractSession(t, userMsg("question"))
	if _, err := named.AppendSessionInfo("contract-name"); err != nil {
		t.Fatal(err)
	}
	m := contractMode(t, named, false)
	m.renderSessionEntries()
	if got := stripANSITest(strings.Join(m.statusLine.Render(100), "\n")); !strings.Contains(got, "contract-name") {
		t.Fatalf("footer lacks the session name:\n%s", got)
	}
	m.opts.SessionHandle = &recordingCompactHandle{inner: contractSession(t, userMsg("other"))}
	m.renderSessionEntries()
	if got := stripANSITest(strings.Join(m.statusLine.Render(100), "\n")); strings.Contains(got, "contract-name") {
		t.Fatalf("footer kept the replaced session's name:\n%s", got)
	}
}
