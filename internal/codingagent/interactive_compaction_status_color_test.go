package codingagent

import (
	"strings"
	"testing"
)

// interactive-mode.ts at Pi 77332f7a (dbc7ff87): compaction_start and the summarization retry show the compaction indicator with the editor's border color as its color function when the editor embeds status.
func TestCompactionStatusIndicatorInEmbeddedEditorUsesTheEditorBorderColor(t *testing.T) {
	m, _, _ := newFrontendEditorProbe(t)
	m.editor.EmbedWorkingStatus = true // interactive.go creates the editor with EmbedWorkingStatus
	custom := func(text string) string { return "\x1b[38;5;196m" + text + "\x1b[39m" }
	m.editor.BorderColor = custom
	m.showCompactionStatusIndicator("manual")
	row := m.editor.Render(80)[0]
	if message := "Compacting context... (escape to cancel)"; !strings.Contains(row, custom(message)) {
		t.Fatalf("border row %q does not draw %q with the editor border color", row, message)
	}
	m.clearStatusIndicator("")
}
