package codingagent

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

type agentMessage = agent.AgentMessage

// sessionChromeMode is an interactive mode on a saved, named two-message
// session whose footer reports 100 context tokens per message.
func sessionChromeMode(t *testing.T) *InteractiveMode {
	t.Helper()
	m := resumeThinkingMode(t, false, userMsg("question"), assistantMsg("", ai.TextContent{Text: "answer"}))
	prepareAssistantEventTest(t, m)
	m.editor = tui.NewEditor()
	m.opts.SessionDir = t.TempDir()
	m.opts.CWD = t.TempDir()
	bindReplacementTestHandle(t, m)
	m.opts.ContextUsage = func() (*int, int) {
		messages := 0
		for _, entry := range m.currentSession().GetEntries() {
			if entry.Base().Type == "message" {
				messages++
			}
		}
		tokens := messages * 100
		return &tokens, 10000
	}
	sc := m.buildSlashContext(context.Background())
	sc.Args = "My Session"
	if err := nameHandler(sc); err != nil {
		t.Fatal(err)
	}
	m.refreshFooterContextUsage()
	return m
}

func renderedPlain(c interface{ Render(int) []string }) []string {
	lines := c.Render(120)
	for i := range lines {
		lines[i] = strings.TrimRight(stripANSITest(lines[i]), " ")
	}
	return lines
}

// Upstream /clone replaces the runtime, and the session change redraws the
// transcript from the clone (renderCurrentSessionState,
// interactive-mode.ts:2115-2124) before "Cloned to new session"
// (5379-5398). Lines from before the clone do not survive.
func TestInteractiveCloneRedrawsTranscriptFromClone(t *testing.T) {
	m := sessionChromeMode(t)
	m.chatContainer.Add(tui.NewText("STALE TRANSCRIPT LINE"))
	if err := cloneHandler(m.buildSlashContext(context.Background())); err != nil {
		t.Fatal(err)
	}
	chat := strings.Join(renderedPlain(m.chatContainer), "\n")
	if strings.Contains(chat, "STALE TRANSCRIPT LINE") {
		t.Fatalf("the transcript from before /clone survived:\n%s", chat)
	}
	for _, want := range []string{"question", "answer", "Cloned to new session"} {
		if !strings.Contains(chat, want) {
			t.Fatalf("transcript after /clone lacks %q:\n%s", want, chat)
		}
	}
}

// Upstream's footer reads the session name and the context estimate from the
// current session on every render, so after /new it shows neither the old
// name nor the old usage.
func TestInteractiveNewSessionResetsFooter(t *testing.T) {
	m := sessionChromeMode(t)
	before := strings.Join(renderedPlain(m.statusLine), "\n")
	if !strings.Contains(before, "My Session") || !strings.Contains(before, "2.0%") {
		t.Fatalf("fixture footer lacks the name or usage:\n%s", before)
	}
	if err := newHandler(m.buildSlashContext(context.Background())); err != nil {
		t.Fatal(err)
	}
	after := strings.Join(renderedPlain(m.statusLine), "\n")
	if strings.Contains(after, "My Session") || !strings.Contains(after, "0.0%") {
		t.Fatalf("footer after /new kept the old session:\n%s", after)
	}
}

// Resuming an unnamed session after a named one shows no name: upstream's
// footer reads the name of the session it renders.
func TestInteractiveResumeShowsResumedSessionName(t *testing.T) {
	m := sessionChromeMode(t)
	other, err := NewSessionManagerWithDir(m.opts.CWD, t.TempDir()).Create("unnamed", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []agentMessage{userMsg("other question"), assistantMsg("", ai.TextContent{Text: "other answer"})} {
		if _, err := other.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.buildSlashContext(context.Background()).LoadSessionPath(other.Path()); err != nil {
		t.Fatal(err)
	}
	if footer := strings.Join(renderedPlain(m.statusLine), "\n"); strings.Contains(footer, "My Session") {
		t.Fatalf("footer after resuming an unnamed session kept the old name:\n%s", footer)
	}
}

// Upstream handleClearCommand adds Spacer(1) and Text(accent "✓ New session
// started", 1, 1) to the cleared transcript (interactive-mode.ts:6657-6670).
func TestInteractiveNewSessionStartedLine(t *testing.T) {
	m := sessionChromeMode(t)
	if err := newHandler(m.buildSlashContext(context.Background())); err != nil {
		t.Fatal(err)
	}
	if got, want := renderedPlain(m.chatContainer), []string{"", "", " ✓ New session started", ""}; !slices.Equal(got, want) {
		t.Fatalf("transcript after /new = %q, want %q", got, want)
	}
}
