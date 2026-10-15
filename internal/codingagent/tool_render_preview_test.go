package codingagent

import (
	"strings"
	"testing"
	"time"
)

// bash.ts:20-30 formatDuration: a duration of a minute or more reads `1m 15s`, one of an hour `1h 2m 3s`; the preview body of a
// subprocess extension's tool result shows the same Took footer as the shell renderers.
func TestPreviewBodyRendererFooterUsesPisDurationFormat(t *testing.T) {
	for elapsed, want := range map[time.Duration]string{
		1500 * time.Millisecond:                     "Took 1.5s",
		75 * time.Second:                            "Took 1m 15s",
		(time.Hour + 2*time.Minute + 3*time.Second): "Took 1h 2m 3s",
	} {
		lines := makePreviewBodyRenderer("full\noutput", "short", elapsed)(80, false)
		if text := stripANSITest(strings.Join(lines, "\n")); !strings.Contains(text, want) {
			t.Errorf("elapsed %v: footer missing %q in:\n%s", elapsed, want, text)
		}
	}
}
