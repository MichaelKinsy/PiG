package codingagent

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// widthTableText is long, space-separated prose that forces wrapping or
// truncation at every narrow width.
const widthTableText = "This selector text is intentionally long so that every narrow width must wrap or truncate it before rendering"

func widthTableSessionSelector(t *testing.T) *sessionSelector {
	t.Helper()
	now := time.Now()
	sessions := []SessionInfo{
		{Path: "/tmp/a.jsonl", Name: "a session with a long user-set name for narrow widths", CWD: "/very/long/project/path/for/width/tests", Modified: now, MessageCount: 42},
		{Path: "/tmp/b.jsonl", FirstMessage: widthTableText, Modified: now.Add(-48 * time.Hour), MessageCount: 7},
	}
	for i := range 14 {
		sessions = append(sessions, SessionInfo{Path: "/tmp/extra.jsonl", FirstMessage: widthTableText, Modified: now.Add(-time.Duration(i) * time.Hour)})
	}
	load := func() ([]SessionInfo, error) { return sessions, nil }
	rename := func(string, string) error { return nil }
	sel := newSessionSelector(load, load, rename, nil, "/tmp/current.jsonl", DefaultKeybindingsManager())
	sel.showPath = true
	return sel
}

// widthTableRenderer is the render half of tui.Component; the login header
// renderer has no Invalidate.
type widthTableRenderer interface{ Render(width int) []string }

// TestInteractiveComponentsNeverExceedRenderWidth renders the interactive
// selectors built in this package at widths 1..120 and requires every line to
// fit. Upstream's TUI stops on a line wider than the terminal, so these must
// stay within the render width.
func TestInteractiveComponentsNeverExceedRenderWidth(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T) widthTableRenderer
	}{
		{"SessionSelector", func(t *testing.T) widthTableRenderer {
			sel := widthTableSessionSelector(t)
			sel.status = widthTableText
			sel.confirmDelete = "/tmp/a.jsonl"
			return sel
		}},
		{"SessionSelectorEmpty", func(t *testing.T) widthTableRenderer {
			none := func() ([]SessionInfo, error) { return nil, nil }
			return newSessionSelector(none, none, nil, nil, "", DefaultKeybindingsManager())
		}},
		{"SessionSelectorRename", func(t *testing.T) widthTableRenderer {
			sel := widthTableSessionSelector(t)
			sel.enterRenameMode()
			if !sel.renameMode {
				t.Fatal("rename mode did not open")
			}
			sel.renameInput.SetText(widthTableText)
			return sel
		}},
		{"AutomaticThemeMenu", func(t *testing.T) widthTableRenderer {
			return newAutomaticThemeMenu("light-theme-with-a-long-name", "dark-theme-with-a-long-name")
		}},
		{"StatusLine", func(t *testing.T) widthTableRenderer {
			model := &ai.Model{ID: "model-with-a-long-identifier", DisplayName: "Model", Capabilities: ai.ModelCapabilities{ContextWindow: 200000}}
			return NewStatusLine(model, "agent-with-a-long-name", nil)
		}},
		{"LoginHeader", func(t *testing.T) widthTableRenderer {
			return newLoginHeaderRenderer(loginHeaderFixture(t), LoginHeaderOptions{TrueColor: true})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for width := 1; width <= 120; width++ {
				for i, line := range tc.build(t).Render(width) {
					if got := widthx.VisibleWidth(line); got > width {
						t.Fatalf("width %d: line %d is %d cells wide: %q", width, i, got, strings.TrimRight(line, " "))
					}
				}
			}
		})
	}
}
