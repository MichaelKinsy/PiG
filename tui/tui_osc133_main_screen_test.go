package tui

import (
	"slices"
	"strings"
	"testing"
)

// TestMainScreenDropsOSC133PromptMarkers ports "does not write OSC 133 prompt markers to the terminal" (Pi 19ce8059, #11519;
// packages/tui/test/tui-render.test.ts "TUI OSC 133 markers"). Terminals read 133;A as a new shell prompt and drop the OSC 7501
// program status, so regular mode writes none, with BEL and ST terminators, in a full render and a differential render, with and
// without an overlay.
func TestMainScreenDropsOSC133PromptMarkers(t *testing.T) {
	run := func(t *testing.T, overlay bool) {
		w := &upstreamBoundedWriteTerminal{}
		ui := NewWithOutput(w, 80, 24)
		c := &recordingComponent{lines: []string{"\x1b]133;A\x07prompt", "\x1b]133;B\x07\x1b]133;C\x07output"}}
		ui.Add(c)
		if overlay {
			ui.ShowOverlay(&recordingComponent{lines: []string{"ov"}}, OverlayOptions{})
		}
		ui.Render()
		c.lines = append(slices.Clone(c.lines), "\x1b]133;A\x1b\\reply", "\x1b]133;B\x1b\\\x1b]133;C\x1b\\done")
		ui.Render()
		output := strings.Join(w.writes, "")
		upstreamExcludes(t, output, "\x1b]133;")
		for _, text := range []string{"prompt", "output", "reply", "done"} {
			upstreamContains(t, output, text)
		}
	}
	t.Run("without an overlay", func(t *testing.T) { run(t, false) })
	t.Run("under an overlay", func(t *testing.T) { run(t, true) })
	t.Run("a stripped line is unchanged across frames", func(t *testing.T) {
		w := &upstreamBoundedWriteTerminal{}
		ui := NewWithOutput(w, 80, 24)
		c := &recordingComponent{lines: []string{"\x1b]133;A\x07prompt"}}
		ui.Add(c)
		ui.Render()
		w.writes = nil
		c.lines = []string{"\x1b]133;A\x1b\\prompt"}
		ui.Render()
		upstreamExcludes(t, strings.Join(w.writes, ""), "prompt")
	})
}
