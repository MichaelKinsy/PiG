package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type initialMessagesCall struct {
	Call    string   `json:"call"`
	Entries []string `json:"entries"`
	Options struct {
		UpdateFooter    bool `json:"updateFooter"`
		PopulateHistory bool `json:"populateHistory"`
	} `json:"options"`
	Message string `json:"message"`
}

type initialMessagesProbeEntry struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type initialMessagesProbe struct {
	ContextEntries []initialMessagesProbeEntry `json:"contextEntries"`
	AllEntries     []initialMessagesProbeEntry `json:"allEntries"`
}

// untrustedResumedMode is a resumed mode whose project holds resources that need trust and is not trusted, so the trust warning is painted.
func untrustedResumedMode(t *testing.T, build func(sess *Session)) *InteractiveMode {
	t.Helper()
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, CONFIG_DIR_NAME, "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := resumedModeWithOutputPad(t, 1, build)
	m.opts.CWD = cwd
	m.opts.SettingsManager = NewSettingsManagerWithProjectTrust(cwd, t.TempDir(), false)
	m.opts.ResumePath = m.currentSession().Path()
	m.editor = tui.NewEditor()
	return m
}

// chatLineIndex is the index of the chat row whose text, without the padding, is exactly text, or -1.
func chatLineIndex(chat, text string) int {
	for i, line := range strings.Split(chat, "\n") {
		if strings.TrimSpace(line) == text {
			return i
		}
	}
	return -1
}

// compactionEntriesInFile counts the compaction entries of a session file by its JSON lines: an independent denominator for the Session's own count.
func compactionEntriesInFile(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for line := range strings.SplitSeq(string(data), "\n") {
		var entry struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Type == "compaction" {
			n++
		}
	}
	return n
}

// renderInitialMessages (interactive-mode.ts) against pinned Pi: the context entries are rendered with updateFooter and populateHistory, then the project-trust warning is
// painted, then "Session compacted 1 time" or "Session compacted N times" is shown when the file holds a compaction entry, counting every compaction entry of the file.
// Pi's call order and status text come from running its method over a recording `this`; the Pig run must leave the transcript, the warning and the status in that order,
// with the status text Pi produced and none where Pi shows none.
func TestRenderInitialMessagesMatchesPi(t *testing.T) {
	counts := []int{0, 1, 2, 3, 9, 10, 11, 100}
	var probes []initialMessagesProbe
	for _, n := range counts {
		probe := initialMessagesProbe{ContextEntries: []initialMessagesProbeEntry{{ID: "u1", Type: "message"}, {ID: "a1", Type: "message"}}}
		probe.AllEntries = append(probe.AllEntries, probe.ContextEntries...)
		for i := range n {
			probe.AllEntries = append(probe.AllEntries, initialMessagesProbeEntry{ID: fmt.Sprintf("c%d", i), Type: "compaction"}, initialMessagesProbeEntry{ID: fmt.Sprintf("m%d", i), Type: "message"})
		}
		probes = append(probes, probe)
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/initial_messages.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want [][]initialMessagesCall
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for i, n := range counts {
		var order []string
		var statuses []string
		for _, call := range want[i] {
			switch call.Call {
			case "renderSessionEntries":
				if !call.Options.UpdateFooter || !call.Options.PopulateHistory {
					t.Fatalf("Pi renders the initial entries with %+v, want updateFooter and populateHistory", call.Options)
				}
				order = append(order, "entries")
			case "renderProjectTrustWarningIfNeeded":
				order = append(order, "trust")
			case "showStatus":
				order = append(order, "status")
				statuses = append(statuses, call.Message)
			}
		}
		wantOrder := "entries,trust"
		if n > 0 {
			wantOrder += ",status"
		}
		if got := strings.Join(order, ","); got != wantOrder {
			t.Fatalf("%d compactions: Pi's call order = %s, want %s", n, got, wantOrder)
		}

		m := untrustedResumedMode(t, func(sess *Session) {
			compactedSession(t, n)(sess)
			if n == 0 {
				if _, err := sess.AppendMessage(assistantMsg("", ai.TextContent{Text: "answer"})); err != nil {
					t.Fatal(err)
				}
			}
		})
		if got := compactionEntriesInFile(t, m.currentSession().Path()); got != n {
			t.Fatalf("the fixture holds %d compaction entries, want %d", got, n)
		}
		m.renderInitialMessages()
		chat := renderedChat(m)
		transcript, warning := strings.Index(chat, "first question"), strings.Index(chat, "This project is not trusted.")
		if transcript < 0 || warning < transcript {
			t.Errorf("%d compactions: want the transcript (at %d) before the trust warning (at %d):\n%s", n, transcript, warning, chat)
		}
		if n == 0 {
			if strings.Contains(chat, "Session compacted") {
				t.Errorf("0 compactions: the transcript shows a compaction status:\n%s", chat)
			}
			continue
		}
		if len(statuses) != 1 {
			t.Fatalf("%d compactions: Pi shows %d statuses, want 1", n, len(statuses))
		}
		if got := strings.Count(chat, "Session compacted"); got != 1 {
			t.Errorf("%d compactions: %d compaction statuses in the transcript, want 1:\n%s", n, got, chat)
		}
		warningLine := strings.Count(chat[:warning], "\n")
		if status := chatLineIndex(chat, statuses[0]); status < warningLine {
			t.Errorf("%d compactions: want Pi's status %q exactly once, as a row (row %d) after the trust warning (row %d):\n%s", n, statuses[0], status, warningLine, chat)
		}
	}
}

// The compaction count is every compaction entry of the file: a compaction on a branch the leaf left still counts, though the context entries that are painted
// no longer hold it (interactive-mode.ts renderInitialMessages reads sessionManager.getEntries(), not the context entries).
func TestRenderInitialMessagesCountsEveryCompactionOfTheFile(t *testing.T) {
	var firstID string
	m := untrustedResumedMode(t, func(sess *Session) {
		id, err := sess.AppendMessage(userMsg("first question"))
		if err != nil {
			t.Fatal(err)
		}
		firstID = id
		if _, err := sess.AppendCompaction("summary", id, 100, nil, false, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := sess.AppendMessage(assistantMsg("", ai.TextContent{Text: "abandoned answer"})); err != nil {
			t.Fatal(err)
		}
		if err := sess.SetLeafID(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := sess.AppendMessage(assistantMsg("", ai.TextContent{Text: "kept answer"})); err != nil {
			t.Fatal(err)
		}
	})
	if firstID == "" {
		t.Fatal("the fixture did not build")
	}
	session := m.currentSession()
	for _, entry := range session.GetBranch() {
		if entry.Base().Type == "compaction" {
			t.Fatal("the leaf's branch holds the compaction: the fixture does not abandon it")
		}
	}
	if got := compactionEntriesInFile(t, session.Path()); got != 1 {
		t.Fatalf("the file holds %d compaction entries, want 1", got)
	}
	m.renderInitialMessages()
	chat := renderedChat(m)
	if chatLineIndex(chat, "Session compacted 1 time") < 0 {
		t.Errorf("a compaction on an abandoned branch is not counted:\n%s", chat)
	}
	if strings.Contains(chat, "abandoned answer") || !strings.Contains(chat, "kept answer") {
		t.Errorf("the transcript is not the leaf's context:\n%s", chat)
	}
}

// renderInitialMessages paints after the rows already in the chat, such as the loaded resources, without clearing them, and a Session without entries paints no
// transcript and no status but still the trust warning of the project.
func TestRenderInitialMessagesKeepsTheLoadedRowsAndPaintsAnEmptySession(t *testing.T) {
	m := untrustedResumedMode(t, func(sess *Session) { compactedSession(t, 1)(sess) })
	m.chatContainer.Add(tui.NewText("LOADED-RESOURCES-ROW"))
	m.renderInitialMessages()
	chat := renderedChat(m)
	resources, transcript := strings.Index(chat, "LOADED-RESOURCES-ROW"), strings.Index(chat, "first question")
	if resources != 0 || transcript < 0 || transcript < resources {
		t.Errorf("want the loaded row first and kept, then the transcript:\n%s", chat)
	}

	empty := untrustedResumedMode(t, func(*Session) {})
	empty.renderInitialMessages()
	got := renderedChat(empty)
	if strings.Contains(got, "Session compacted") || !strings.Contains(got, "This project is not trusted.") {
		t.Errorf("an empty Session paints the trust warning alone:\n%s", got)
	}
	if strings.Contains(got, "first question") {
		t.Errorf("an empty Session paints a transcript:\n%s", got)
	}
}
