package codingagent

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
)

// interactive-mode.ts:1228-1250 shows a failed prompt() through showError (4583-4587): a spacer, then `Error: <message>` in the theme's error color. prompt() throws formatNoModelSelectedMessage() when no model is selected, so that guidance also reads `Error: ...`, not a bare warning-colored line.
func TestShowTurnErrorRendersThroughShowErrorInTheThemeErrorColor(t *testing.T) {
	tests := []struct {
		name string
		err  error
		text string
	}{
		{"failed prompt", errors.New("boom"), "Error: boom"},
		{"no model selected", agent.ErrNoModelSelected, "Error: " + FormatNoModelSelectedMessage()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir()})
			m.chatContainer = tui.NewContainer()
			m.tuiInst = tui.NewWithOutput(&out, 100, 30)
			m.tuiInst.Add(m.chatContainer)
			m.runCtx = context.Background()
			m.showTurnError(tc.err)
			for len(m.uiTaskCh) > 0 {
				(<-m.uiTaskCh)()
			}
			lines := m.chatContainer.Render(100)
			joined := strings.Join(lines, "\n")
			first, _, _ := strings.Cut(tc.text, "\n")
			if want := tui.ActiveTheme().Fg("error", first); !strings.Contains(joined, strings.TrimSuffix(want, tui.SGRFgReset)) {
				t.Errorf("chat lacks the error-colored %q in:\n%q", first, joined)
			}
			if strings.Contains(joined, "\x1b[31m") || strings.Contains(joined, "\x1b[33m") {
				t.Errorf("fixed ANSI colors leaked into the error row: %q", joined)
			}
		})
	}
}

// interactive-mode.ts:4153-4166 addCacheMissNotice: a spacer, then the notice in the theme warning color at padding 1; the live path and the rebuilt transcript share it.
func TestCacheMissNoticeIsASpacerThenAThemeWarningRow(t *testing.T) {
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir()})
	m.chatContainer = tui.NewContainer()
	m.addCacheMissNotice("Cache miss: 50k tokens re-billed")
	lines := m.chatContainer.Render(60)
	if len(lines) != 2 || strings.TrimSpace(lines[0]) != "" {
		t.Fatalf("want a blank row then the notice, got %q", lines)
	}
	if want := " " + tui.ActiveTheme().Fg("warning", "Cache miss: 50k tokens re-billed"); !strings.HasPrefix(lines[1], want) {
		t.Errorf("notice row = %q, want prefix %q", lines[1], want)
	}
}
