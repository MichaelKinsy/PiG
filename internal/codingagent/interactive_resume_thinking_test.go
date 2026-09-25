package codingagent

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// resumeThinkingMode persists messages to a session file, reloads the file
// from disk (the resume path), and returns a mode ready to render it.
func resumeThinkingMode(t *testing.T, hide bool, messages ...agent.AgentMessage) *InteractiveMode {
	t.Helper()
	dir := t.TempDir()
	sm := NewSessionManagerWithDir(dir, dir)
	sess, err := sm.Create("resume-thinking", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range messages {
		if _, err := sess.AppendMessage(msg); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := NewSessionManagerWithDir(dir, dir).Load(sess.Path())
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	var terminal bytes.Buffer
	m := &InteractiveMode{
		opts:          InteractiveOptions{SessionHandle: &recordingCompactHandle{inner: loaded}},
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(&terminal, 80, 30),
		toolByID:      make(map[string]*tui.ToolExecutionComponent),
		toolStarts:    make(map[string]time.Time),
		hideThinking:  hide,
		outputPad:     1,
	}
	m.tuiInst.Add(m.chatContainer)
	return m
}

func userMsg(text string) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: text}}}}
}

func assistantMsg(thinking string, content ...ai.AssistantContentBlock) agent.AgentMessage {
	return agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role: agent.RoleAssistant, Content: content, Thinking: thinking, StopReason: ai.StopReasonStop,
	}}
}

func renderedChat(m *InteractiveMode) string {
	return stripANSITest(strings.Join(m.chatContainer.Render(80), "\n"))
}

// Upstream renderInitialMessages/rebuildChatFromMessages render each assistant
// message through AssistantMessageComponent, which reads thinking from the
// persisted content blocks. Pig's runtime-only AssistantMessage.Thinking is
// not serialized, so a rebuild must not depend on it.
func TestInteractiveMode_ResumeRendersPersistedThinking(t *testing.T) {
	m := resumeThinkingMode(t, false,
		userMsg("question"),
		assistantMsg("PERSISTED_THOUGHT",
			ai.ThinkingContent{Thinking: "PERSISTED_THOUGHT"},
			ai.TextContent{Text: "FINAL_ANSWER"}),
	)
	m.renderSessionEntries()
	got := renderedChat(m)
	if !strings.Contains(got, "PERSISTED_THOUGHT") {
		t.Fatalf("resumed chat dropped persisted thinking:\n%s", got)
	}
	if strings.Index(got, "PERSISTED_THOUGHT") > strings.Index(got, "FINAL_ANSWER") {
		t.Fatalf("thinking must render before the answer:\n%s", got)
	}

	m.toggleThinkingVisibility()
	hidden := renderedChat(m)
	if strings.Contains(hidden, "PERSISTED_THOUGHT") || !strings.Contains(hidden, "Thinking...") {
		t.Fatalf("hide-thinking toggle did not collapse resumed thinking:\n%s", hidden)
	}
}

func TestInteractiveMode_ResumeHonorsHideThinkingSetting(t *testing.T) {
	m := resumeThinkingMode(t, true,
		userMsg("question"),
		assistantMsg("", ai.ThinkingContent{Thinking: "ONLY_THINKING"}),
	)
	m.renderSessionEntries()
	got := renderedChat(m)
	if strings.Contains(got, "ONLY_THINKING") || !strings.Contains(got, "Thinking...") {
		t.Fatalf("hidden thinking-only message should show the hidden label:\n%s", got)
	}
	m.toggleThinkingVisibility()
	if got := renderedChat(m); !strings.Contains(got, "ONLY_THINKING") {
		t.Fatalf("revealing thinking on a resumed thinking-only message showed nothing:\n%s", got)
	}
}

// AssistantMessageComponent.updateContent renders content in order, trims
// blocks, joins consecutive thinking blocks with a blank line, and adds a
// spacer after a thinking run only when visible content follows.
func TestInteractiveMode_ResumeThinkingFollowsContentOrder(t *testing.T) {
	m := resumeThinkingMode(t, false,
		userMsg("question"),
		assistantMsg("",
			ai.ThinkingContent{Thinking: "  THINK_A  "},
			ai.ThinkingContent{Thinking: "THINK_B\n"},
			ai.TextContent{Text: "TEXT_C"},
			ai.ThinkingContent{Thinking: "THINK_D"},
			ai.ThinkingContent{Thinking: "   "},
		),
	)
	m.renderSessionEntries()
	if len(m.assistantBlocks) != 1 {
		t.Fatalf("assistant blocks = %d, want 1", len(m.assistantBlocks))
	}
	lines := m.assistantBlocks[0].Render(80)
	for i := range lines {
		lines[i] = strings.TrimRight(stripANSITest(lines[i]), " ")
	}
	want := []string{"", " THINK_A", "", " THINK_B", "", " TEXT_C", " THINK_D"}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rendered lines:\n%q\nwant:\n%q", lines, want)
	}
}

// Upstream addMessageToChat adds Spacer(1) before every user message once the
// chat has children, in addition to the user box's own vertical padding.
func TestInteractiveMode_ResumeSpacesUserMessageAfterAssistant(t *testing.T) {
	m := resumeThinkingMode(t, false,
		userMsg("FIRST_USER"),
		assistantMsg("", ai.TextContent{Text: "ANSWER"}, ai.ThinkingContent{Thinking: "TRAILING_THOUGHT"}),
		userMsg("SECOND_USER"),
	)
	m.renderSessionEntries()
	lines := m.chatContainer.Render(40)
	for i := range lines {
		lines[i] = strings.TrimSpace(stripANSITest(lines[i]))
	}
	want := []string{"", "FIRST_USER", "", "", "ANSWER", "TRAILING_THOUGHT", "", "", "SECOND_USER", ""}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("resumed chat lines:\n%q\nwant:\n%q", lines, want)
	}
}
