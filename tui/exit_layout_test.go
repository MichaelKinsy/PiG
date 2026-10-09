package tui

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TuiMainScreen.beforeTerminalStop uses previousLines.length, not the last
// rendered row or editor cursor row, then CRLF. Preserve-screen swaps skip it.
func TestTUIStopParksBelowPreviousLines(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		lines, cursor, hardware int
		preserve                bool
		want                    string
	}{
		{name: "empty", want: "\x1b[?25h"},
		{name: "one-row", lines: 1, want: " \x1b[1B\r\n\x1b[?25h"},
		{name: "editor-before-footer", lines: 8, cursor: 7, hardware: 4, want: " \x1b[4B\r\n\x1b[?25h"},
		{name: "scrolled-transcript", lines: 1000, cursor: 999, hardware: 996, want: " \x1b[4B\r\n\x1b[?25h"},
		{name: "hardware-below-content", lines: 2, cursor: 1, hardware: 4, want: " \x1b[2A\r\n\x1b[?25h"},
		{name: "preserve-screen", lines: 8, cursor: 7, hardware: 4, preserve: true, want: "\x1b[?25h"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			ui := NewWithOutput(&output, 80, 24)
			ui.RestoreRenderState(TuiMainScreenRenderState{PreviousLines: make([]string, tc.lines), CursorRow: tc.cursor, HardwareCursorRow: tc.hardware})
			ui.StopWithOptions(StopOptions{PreserveScreen: tc.preserve})
			if got := output.String(); got != tc.want {
				t.Fatalf("stop bytes = %q, want Pi %q", got, tc.want)
			}
		})
	}
}

// Pi: packages/coding-agent/src/modes/interactive/components/armin.ts:84 (ArminComponent.invalidate); packages/coding-agent/src/modes/interactive/components/assistant-message.ts:51 (AssistantMessageComponent.invalidate); packages/coding-agent/src/modes/interactive/components/bash-execution.ts:75 (BashExecutionComponent.invalidate); packages/coding-agent/src/modes/interactive/components/branch-summary-message.ts:27 (BranchSummaryMessageComponent.invalidate); packages/coding-agent/src/modes/interactive/components/compaction-summary-message.ts:27 (CompactionSummaryMessageComponent.invalidate); packages/coding-agent/src/modes/interactive/components/skill-invocation-message.ts:28 (SkillInvocationMessageComponent.invalidate).
func TestCustomMessageBackgroundTracksTheme(t *testing.T) {
	previous := ActiveTheme().Name
	t.Cleanup(func() { SetTheme(previous) })
	components := []Component{
		NewCustomMessageComponent(&CustomMessage{CustomType: "notice", Content: "finished"}, nil, nil, 1),
		NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: "summary", TokensBefore: 100}, nil, 1),
		NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: "summary"}, nil, 1),
		NewSkillInvocationMessageComponent(ParsedSkillBlock{Name: "probe", Content: "body"}, nil, 1),
	}
	for _, theme := range []string{"dark", "light", "dark"} {
		SetTheme(theme)
		for _, component := range components {
			component.Invalidate()
			for _, line := range component.Render(40) {
				if line != "" && !strings.HasPrefix(line, ActiveTheme().CustomMessageBg) {
					t.Errorf("%T in %s theme hard-codes the message background: %q", component, theme, line)
					break
				}
			}
		}
	}
}

// CustomMessageComponent adds Spacer(1) before its Box(1,1). The Box's top
// and bottom rows retain the custom-message background even with empty text.
func TestCustomMessageRetainsBoxVerticalPadding(t *testing.T) {
	for _, content := range []string{"", "finished", "first\nsecond"} {
		t.Run(fmt.Sprintf("%q", content), func(t *testing.T) {
			component := NewCustomMessageComponent(&CustomMessage{CustomType: "notice", Content: content}, nil, nil, 1)
			lines := component.Render(40)
			wantText := []string{"", strings.Repeat(" ", 40), " [notice]" + strings.Repeat(" ", 31), strings.Repeat(" ", 40)}
			if content != "" {
				for line := range strings.SplitSeq(content, "\n") {
					wantText = append(wantText, " "+line+strings.Repeat(" ", 39-len(line)))
				}
			}
			wantText = append(wantText, strings.Repeat(" ", 40))
			text := make([]string, len(lines))
			for i, line := range lines {
				text[i] = stripANSI(line)
			}
			if !slices.Equal(text, wantText) {
				t.Fatalf("custom message rows = %q, want %q", text, wantText)
			}
			padding := paintBgWith(ActiveTheme().CustomMessageBg, "", 40)
			if lines[1] != padding || lines[len(lines)-1] != padding {
				t.Fatalf("box padding does not retain its background: %q", lines)
			}
		})
	}
}
