package codingagent

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Each command below is entered the way the editor's Enter reaches it (the owner's slash dispatch for /new, /clone and /reload; for /fork, /resume and /tree the action their picker calls with the chosen
// row, and the compaction as the agent's event), on a terminal that records every byte. The transcript fits the pane. Pi's rule for each is the same: the command clears the chat
// container and renders the new transcript, then requests an ordinary render (interactive-mode.ts:2200 and 2311-2319 for /new, /resume, /fork and /clone, 2113-2114 and 5767-5768
// for /tree, 3773-3775 for a compaction, 6141-6150 for a logout, which changes no row), and tui-main-screen.ts:456-458 sends the clearing redraw (ESC[2J ESC[H ESC[3J, :288) only when the first
// changed row is above the viewport. So Pi writes neither ESC[2J nor ESC[3J for any of them, and the lines above the TUI stay. The reload is Pi's one forced frame, the box
// (requestRender(true), :6562), and the transcript repaint that follows writes none.
func TestSessionCommandsKeepTheScrollbackFromTheirCaller(t *testing.T) {
	firstUserEntry := func(m *InteractiveMode) string {
		for _, entry := range m.currentSession().GetEntries() {
			if entry.Base().Type == "message" {
				return entry.Base().ID
			}
		}
		t.Fatal("the fixture session has no message entry")
		return ""
	}
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		run  func(t *testing.T, m *InteractiveMode)
		want string
	}{
		{"/new", func(t *testing.T, m *InteractiveMode) { m.dispatchSlash(ctx, "/new") }, "New session started"},
		{"/clone", func(t *testing.T, m *InteractiveMode) { m.dispatchSlash(ctx, "/clone") }, "Cloned to new session"},
		{"/fork", func(t *testing.T, m *InteractiveMode) {
			// A bare /fork opens the picker, whose chosen row reaches forkHandler as its argument (an interactive "/fork <id>" is not a command, as in Pi).
			sc := m.buildSlashContext(ctx)
			sc.Args = firstUserEntry(m)
			if err := forkHandler(sc); err != nil {
				t.Fatal(err)
			}
			m.tuiInst.Render()
		}, "Forked to new session"},
		{"/resume", func(t *testing.T, m *InteractiveMode) {
			other, err := NewSessionManagerWithDir(m.opts.CWD, t.TempDir()).Create("other", "")
			if err != nil {
				t.Fatal(err)
			}
			for _, message := range []agentMessage{userMsg("other question"), assistantMsg("", ai.TextContent{Text: "other answer"})} {
				if _, err := other.AppendMessage(message); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.buildSlashContext(ctx).LoadSessionPath(other.Path()); err != nil {
				t.Fatal(err)
			}
			m.tuiInst.Render()
		}, "other answer"},
		{"/tree", func(t *testing.T, m *InteractiveMode) {
			if _, err := m.navigateTree(ctx, firstUserEntry(m), false, ""); err != nil {
				t.Fatal(err)
			}
			m.tuiInst.Render()
		}, ""},
		{"/compact", func(t *testing.T, m *InteractiveMode) {
			m.handleAgentEvent(agent.CompactionEndEvent{Reason: "manual", Summary: "SUMMARY", TokensBefore: 200})
			m.tuiInst.Render()
		}, "[compaction]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, terminal := scrollbackMode(t)
			m.slashRegistry = NewSlashRegistry()
			tc.run(t, m)
			requireDifferentialFrame(t, terminal.String(), tc.want)
		})
	}

	t.Run("/reload", func(t *testing.T) {
		m, terminal := scrollbackMode(t)
		m.slashRegistry = NewSlashRegistry()
		m.dispatchSlash(ctx, "/reload")
		m.tuiInst.Render()
		written := terminal.String()
		if !strings.Contains(written, "Reloaded") {
			t.Fatalf("the command did not reload:\n%q", written)
		}
		// One clearing frame, the box's, at the start of the command: everything after it is differential.
		if strings.Count(written, "\x1b[2J") != 1 || strings.Count(written, "\x1b[3J") != 1 || strings.Count(written, scrollbackHeaderRow) != 1 {
			t.Errorf("a reload writes the clearing redraw once, for the box, and repaints differentially after it (2J=%d 3J=%d header rows=%d):\n%q",
				strings.Count(written, "\x1b[2J"), strings.Count(written, "\x1b[3J"), strings.Count(written, scrollbackHeaderRow), written)
		}
		if !strings.HasPrefix(strings.TrimPrefix(written, "\x1b[?2026h"), "\x1b[2J\x1b[H\x1b[3J") {
			t.Errorf("the clearing redraw is not the first frame of the reload:\n%q", written)
		}
	})
}
