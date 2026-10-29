package tui

import (
	"fmt"
	"testing"
)

// Kitty flag-2 key reports reach the alt screen as press and release events. A release must never act as a press, however the terminal's bytes are split across reads (tui.ts handleInput; tui-alt-screen.ts handleViewportInput isKeyRelease).
func TestAltScreenKittyReleaseNeverScrollsWhateverTheReadSplit(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	useAltScreenBindings(t, nil)
	for _, step := range []struct {
		name           string
		press, release string
	}{
		{"pageUp", "\x1b[57421u", "\x1b[57421;1:3u"},
		{"pageUpTildeLegacy", "\x1b[5~", "\x1b[5;1:3~"},
	} {
		stream := step.press + step.release
		for cut := 0; cut <= len(stream); cut++ {
			t.Run(fmt.Sprintf("%s/%d", step.name, cut), func(t *testing.T) {
				h := newAltHarness(t, 20, 8, TuiAltScreenOptions{})
				h.tui.Add(NewText(numberedLines(40)))
				h.start()
				h.render()
				before := h.tui.ViewportTop()
				buffer := NewStdinBuffer(StdinBufferOptions{})
				var chunks []string
				chunks = append(chunks, buffer.ProcessString(stream[:cut])...)
				chunks = append(chunks, buffer.ProcessString(stream[cut:])...)
				chunks = append(chunks, buffer.Flush()...)
				h.send(chunks...)
				got := before - h.tui.ViewportTop()
				if got != 4 {
					t.Fatalf("chunks %q scrolled %d rows from %d, want 4", chunks, got, before)
				}
			})
		}
	}
}
