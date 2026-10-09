package codingagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

const unsavedForkMessage = "This session has not been saved yet. Send a message before cloning or forking it."

// Pi agent-session-runtime.ts:312-316 refuses a persisted but unflushed source before any replacement or write.
func TestInteractiveCloneUnsavedRefusesWithoutWriting(t *testing.T) {
	m := resumeThinkingMode(t, false)
	prepareAssistantEventTest(t, m)
	sm := NewSessionManagerWithDir(t.TempDir(), t.TempDir())
	sess, err := sm.Create("unsaved", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sess.AppendThinkingLevelChange("off"); err != nil {
		t.Fatal(err)
	}
	m.opts.SessionHandle.ReplaceInner(sess)
	m.opts.SessionDir = sm.SessionDir()
	bindReplacementTestHandle(t, m)
	sc := m.buildSlashContext(context.Background())
	_, err = sc.CloneCurrent()
	if err == nil || err.Error() != unsavedForkMessage {
		t.Errorf("clone error = %v", err)
	}
	files, err := os.ReadDir(sm.SessionDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("clone wrote %v", files)
	}
	if m.currentSession() != sess {
		t.Error("replaced source on failure")
	}
}

// Pi interactive-mode.ts:5345, 5393-5394, and showError: persistent padded status/errors, not footer flashes or Markdown path messages.
func TestInteractiveSessionCommandMessages(t *testing.T) {
	m := resumeThinkingMode(t, false)
	prepareAssistantEventTest(t, m)
	sc := m.buildSlashContext(context.Background())
	if err := forkHandler(sc); err != nil {
		t.Fatal(err)
	}
	sc.CloneCurrent = func() (string, error) { return "/unused/clone.jsonl", nil }
	sc.CurrentSession = nil
	sc.SetEditorText = nil
	if err := cloneHandler(sc); err != nil {
		t.Fatal(err)
	}
	m.slashRegistry = NewSlashRegistry()
	m.slashRegistry.Register(BuiltinSlashCommand{Name: "w3-error", Handler: func(*SlashContext) error { return errors.New("command failed") }})
	m.dispatchSlashContext(sc, "/w3-error")
	text := stripANSITest(strings.Join(m.chatContainer.Render(120), "\n"))
	// Consecutive showStatus calls replace the prior status, just as Pi does.
	if !strings.Contains(text, " Cloned to new session") || !strings.Contains(text, " Error: command failed") || strings.Contains(text, "Fork cancelled.") || strings.Contains(text, "/unused") {
		t.Errorf("command output: %q", text)
	}
}

// Pi interactive-mode.ts:2096-2113 and message_end: no duplicate provider footer, one hint for non-retryable non-cancelled errors.
func TestInteractiveAssistantBugHintOnce(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stop    ai.StopReason
		message string
		hint    bool
	}{
		{"ordinary", ai.StopReasonError, "unhandled request", true},
		{"aborted", ai.StopReasonAborted, "unhandled request", false},
		{"retryable", ai.StopReasonError, "overloaded", false},
		{"cancelled", ai.StopReasonError, "Operation CANCELLED", false},
		{"cancel", ai.StopReasonError, "request cancel", false},
		{"boundary", ai.StopReasonError, "cancellation policy failure", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := resumeThinkingMode(t, false)
			prepareAssistantEventTest(t, m)
			m.statusLine.SetStatusHook(m.showStatus)
			msg := assistantMsg("")
			msg.Assistant.StopReason = tc.stop
			msg.Assistant.ErrorMessage = tc.message
			for range 2 {
				m.handleAgentEvent(agent.MessageStartEvent{Message: msg})
				m.handleAgentEvent(agent.MessageEndEvent{Message: msg})
			}
			text := stripANSITest(strings.Join(m.chatContainer.Render(120), "\n"))
			want := 0
			if tc.hint {
				want = 1
			}
			if got := strings.Count(text, " If this looks like a pig bug, /bug sends a report to the developers."); got != want {
				t.Errorf("hint count %d want %d: %q", got, want, text)
			}
			if status := strings.Join(m.statusLine.Render(120), "\n"); strings.Contains(status, "Provider request") {
				t.Errorf("duplicate provider status: %q", status)
			}
		})
	}
}

func TestInteractiveCompactionErrorPadding(t *testing.T) {
	for _, reason := range []string{"manual", "overflow"} {
		t.Run(reason, func(t *testing.T) {
			m := resumeThinkingMode(t, false)
			prepareAssistantEventTest(t, m)
			m.handleAgentEvent(agent.CompactionEndEvent{Reason: reason, ErrorMessage: "Nothing to compact"})
			text := stripANSITest(strings.Join(m.chatContainer.Render(120), "\n"))
			want := " Nothing to compact"
			if reason == "manual" {
				want = " Error: Nothing to compact"
			}
			if !strings.Contains(text, want) {
				t.Errorf("padding: %q want %q", text, want)
			}
		})
	}
}

// Pi refuses to fork a persisted Session whose file does not exist and leaves the source untouched (agent-session-runtime.ts:296-316). Upstream 0.99.1 creates the file at the first user message (session-manager.ts:1166-1185), so only a Session with setup entries alone is unsaved; a non-root fork of a Session that has a user message succeeds. The root-message fresh-session case is unchanged.
func TestForkUnsavedRefusalPreservesSource(t *testing.T) {
	sm := NewSessionManagerWithDir(t.TempDir(), t.TempDir())
	sess, err := sm.Create("unsaved-fork", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendThinkingLevelChange("off"); err != nil {
		t.Fatal(err)
	}
	leaf := *sess.GetLeafID()
	if err := sess.CheckSavedForFork(); err == nil || err.Error() != unsavedForkMessage {
		t.Fatalf("setup-only session: err=%v", err)
	}
	if sm.Current() != sess || *sess.GetLeafID() != leaf {
		t.Fatal("failed check mutated source")
	}
	files, err := os.ReadDir(sm.SessionDir())
	if err != nil || len(files) != 0 {
		t.Fatalf("files=%v err=%v", files, err)
	}
	id, err := sess.AppendMessage(userMsg("first"))
	if err != nil {
		t.Fatal(err)
	}
	if next, text, err := sm.ForkToNewSession(sess, id); err != nil || next == nil || text != "first" {
		t.Fatalf("fork after the first user message=%p text=%q err=%v", next, text, err)
	}
	root, err := sm.Create("root-user", "")
	if err != nil {
		t.Fatal(err)
	}
	id, err = root.AppendMessage(userMsg("root"))
	if err != nil {
		t.Fatal(err)
	}
	if next, text, err := sm.ForkToNewSession(root, id); err != nil || next == nil || text != "root" {
		t.Fatalf("root fork=%p text=%q err=%v", next, text, err)
	}
}

func TestResumeHintExplicitDefaultDirectory(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	dir := defaultSessionDir(cwd)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte("{\"type\":\"session\",\"id\":\"resume\",\"version\":3}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The CLI resolves all three resume forms to the actual session directory.
	// Exercise the shutdown caller, which knows the cwd and must omit its default.
	m := resumeThinkingMode(t, false)
	sess, err := NewSessionManagerWithDir(cwd, dir).Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m.opts.SessionHandle.ReplaceInner(sess)
	m.opts.CWD = cwd
	m.opts.SessionDir = dir
	got := captureStdout(t, m.printResumeHint)
	if strings.Contains(got, "--session-dir") {
		t.Errorf("redundant default dir: %q", got)
	}
}
