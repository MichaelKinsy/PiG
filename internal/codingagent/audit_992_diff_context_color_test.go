package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi colors diff context lines, and lines that do not parse as diff lines, with the theme's toolDiffContext token
// (.upstream/v0.99.2/packages/coding-agent/src/modes/interactive/components/diff.ts:89,141; dark.json maps it to
// "muted"). Found while probing the renderShell case of tool-renderer-examples.test.ts in a 100x35 tmux pane against
// an OpenAI-compatible endpoint that asks for one edit of parity-edit-target.txt: real Pi 0.99.2 draws the context line
// as "\x1b[38;2;157;165;169m 1 before line\x1b[39m" and PiG as SGR dim ("\x1b[2m 1 before line\x1b[22m" from renderDiffString). The interactive
// edit card renders through renderDiffString, which hard-codes SGR dim; tui.RenderDiff uses the token.
// interactive-rendering/10-edit-tool-diff compares the same block with output_normalized_equal, which strips the SGR.
func TestAuditEditDiffContextLinesUseTheToolDiffContextColor(t *testing.T) {
	th := tui.ActiveTheme()
	if th.ToolDiffContext == "" {
		t.Fatal("the active theme has no toolDiffContext color")
	}
	lines := renderDiffString(" 1 before line\n-2 REPLACE_ME\n+2 REPLACED\n 3 after line\nnot a diff line", 100)
	for i, want := range map[int]string{
		0: th.ToolDiffContext + " 1 before line" + tui.SGRFgReset,
		3: th.ToolDiffContext + " 3 after line" + tui.SGRFgReset,
		4: th.ToolDiffContext + "not a diff line" + tui.SGRFgReset,
	} {
		if i >= len(lines) || lines[i] != want {
			t.Errorf("line %d = %q, want %q", i, auditLineAt(lines, i), want)
		}
	}
}

func auditLineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<missing>"
}
