//go:build !pig_strip_syntax_highlight

package codingagent

import (
	"slices"
	"strings"
	"testing"
)

// A write call that streams in a file warms the highlight of the whole file a step at a time, so the frame that completes the call
// continues from the last step. The lines that frame draws, collapsed and expanded, are those of a call that highlights the whole file
// once, as Pi's does.
func TestWarmedWriteCardDrawsTheLinesOfAWholeRun(t *testing.T) {
	content := streamedWriteTypeScript(120)

	forgetHighlights(t)
	_, _, cold := streamWriteCard(t, content, len(content)+1) // no streaming frame: the complete call highlights the file in one run
	if cold.writeWarmed != 0 {
		t.Fatalf("a call that never streamed warmed the highlight to %d bytes", cold.writeWarmed)
	}
	forgetHighlights(t)
	wantCollapsed, wantExpanded, _ := streamWriteCard(t, content, len(content)+1)

	forgetHighlights(t)
	gotCollapsed, gotExpanded, warm := streamWriteCard(t, content, 1000)
	if warm.writeWarmOff || warm.writeWarmed < len(content)-writeWarmStep {
		t.Fatalf("the warm-ups stopped at %d of %d bytes (off: %v)", warm.writeWarmed, len(content), warm.writeWarmOff)
	}
	if !slices.Equal(gotCollapsed, wantCollapsed) {
		t.Errorf("collapsed card differs after warm-ups:\n got %q\nwant %q", gotCollapsed, wantCollapsed)
	}
	if !slices.Equal(gotExpanded, wantExpanded) {
		t.Errorf("expanded card differs after warm-ups (%d lines, want %d)", len(gotExpanded), len(wantExpanded))
	}
	if !strings.Contains(strings.Join(gotCollapsed, "\n"), "more lines") {
		t.Fatalf("the collapsed card names no hidden lines:\n%s", strings.Join(gotCollapsed, "\n"))
	}
}

// These tests select a theme, so TestThemeTestsRestoreTheProcessTheme runs them too; a build that strips what they need does not link them, and neither does it have the entries.
func init() {
	themeRestoringTests["TestWarmedWriteCardDrawsTheLinesOfAWholeRun"] = TestWarmedWriteCardDrawsTheLinesOfAWholeRun
}
