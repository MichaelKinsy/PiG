package tui

import (
	"strings"
	"testing"
)

// Pi 0.87.1 editor.ts render owns borders and content, not the host's widget spacer.
func TestEditorFrameDoesNotOwnHostSpacer(t *testing.T) {
	e := NewEditor()
	rows := e.Render(20)
	if got := stripANSI(rows[0]); got != strings.Repeat("─", 20) {
		t.Fatalf("first editor row = %q; want the top border, not a host spacer", got)
	}
}

// Pi model-selector.ts uses Spacer, which renders an empty string, not padded Text.
func TestModelSelectorSpacersMatchUpstream(t *testing.T) {
	m := NewModelSelector("", nil, []ModelSelectorItem{{Provider: "p", ID: "m", Name: "M"}}, "p/m")
	if row := m.Render(100)[1]; row != "" {
		t.Fatalf("model spacer = %q; want empty Spacer row", row)
	}
}

// Pi tree-selector.ts:1383-1394 owns a leading spacer and keeps the bottom spacer in the empty state.
func TestTreeFrameSpacingAndTitleStyles(t *testing.T) {
	treeHelpTestKeybindings(t, nil)
	ts := NewTreeSelect("", nil)
	rows := ts.Render(100)
	if rows[0] != "" {
		t.Fatalf("tree leading spacer = %q", rows[0])
	}
	want := NewPaddedText("\x1b[1m  Session Tree\x1b[0m", 1, 0, nil).Render(100)[0]
	if rows[2] != want {
		t.Fatalf("title = %q, want %q", rows[2], want)
	}
	if rows[len(rows)-2] != "" {
		t.Fatalf("empty list bottom spacer = %q", rows[len(rows)-2])
	}
}

// The immediate tmux/screen boundary wins over an outer Herdr graphics hint, as terminal-image.ts:77-85 requires.
func TestTmuxImageDetectionPrecedesOuterHerdr(t *testing.T) {
	for _, term := range []string{"tmux-256color", "screen-256color"} {
		t.Run(term, func(t *testing.T) {
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_KITTY_GRAPHICS", "1")
			t.Setenv("TERM", term)
			t.Setenv("TMUX", "")
			t.Setenv("COLORTERM", "truecolor")
			got := detectCapabilitiesFromEnvironment(func() bool { return false }, "linux")
			if got.Images != "" {
				t.Fatalf("%s inherited images %q through multiplexer", term, got.Images)
			}
		})
	}
}
