//go:build !race

package codingagent

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A frame over unchanged history costs what the frame changes plus the children it polls, not the lines of the history: the
// header may not break the window the layout container keeps. The transcript has the same number of children at both sizes and
// 200 times the lines at the larger, so a frame that copies, resets or compares every line of the transcript (the header
// re-rendering every frame does) costs far more at the larger size; one that polls the children costs the same. Wall time
// is compared because a frame that walks lines it already has allocates nothing.
func TestFrameCostOverUnchangedTranscriptDoesNotGrowWithItsLines(t *testing.T) {
	const children, small, large = 64, 2, 400
	for _, mode := range []string{"regular"} {
		frameCost := func(lines int, frame func(m *InteractiveMode)) time.Duration {
			m, _ := headerFrameProbe(t, mode, children, lines)
			best := time.Duration(1 << 62)
			for range 15 {
				start := time.Now()
				for range 20 {
					frame(m)
				}
				best = min(best, time.Since(start)/20)
			}
			return best
		}
		for name, frame := range map[string]func(m *InteractiveMode){
			"idle":      func(m *InteractiveMode) { m.tuiInst.Render() },
			"keystroke": func(m *InteractiveMode) { m.editor.Invalidate(); m.tuiInst.Render() },
		} {
			t.Run(mode+"/"+name, func(t *testing.T) {
				short, long := frameCost(small, frame), frameCost(large, frame)
				t.Logf("a frame costs %s over %d lines and %s over %d lines", short, children*small, long, children*large)
				testenv.TimingGate(t, long > 4*short+50*time.Microsecond, "a frame over %d lines costs %s, %.1fx the %s of one over %d lines: frame work grows with the transcript's lines", children*large, long, float64(long)/float64(short), short, children*small)
			})
		}
	}
}
