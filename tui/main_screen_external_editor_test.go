package tui

import (
	"strings"
	"testing"
)

// Pi's requestRender(true) clears the physical buffer after an external program has written to the terminal; treating the next render as a fresh append leaves the old dialog visible.
func TestMainScreenExternalEditorRepaint(t *testing.T) {
	var output strings.Builder
	ui := NewWithOutput(&output, 80, 24)
	ui.Add(&fixedLinesComponent{lines: []string{"dialog"}})
	ui.Render()
	output.Reset()
	ui.Stop()
	ui.Start()
	ui.RepaintAll()
	ui.Stop()
	if !strings.Contains(output.String(), "\x1b[2J") || !strings.Contains(output.String(), "dialog") {
		t.Fatalf("external-editor repaint did not clear and restore the dialog: %q", output.String())
	}
}

func BenchmarkEditorRemoteCachedFrame(b *testing.B) {
	e := NewEditor()
	e.SetRemote(&recordingRemote{})
	e.SetRemoteFrame([]string{strings.Repeat("─", 120), "draft text", strings.Repeat("─", 120)}, 120, false)
	b.ReportAllocs()
	for b.Loop() {
		e.Render(120)
	}
}
