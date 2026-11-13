//go:build !race

package codingagent

import (
	"testing"
	"testing/synctest"
)

// pig additive (D614): with the terminal view client selected and no mount, a frame allocates what it allocates
// without the client. The race detector changes allocation counts, so the comparison runs without it.
func TestTerminalViewClientNeverMountedAllocatesNothingPerFrame(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				frame := func(selected bool) float64 {
					p := newViewModeProbe(t, mode, selected)
					p.typeText("hello")
					allocs := testing.AllocsPerRun(20, func() { p.m.tuiInst.Render() })
					p.term.take()
					return allocs
				}
				off, on := frame(false), frame(true)
				if on != off {
					t.Errorf("allocations of one frame: off %v, on %v", off, on)
				}
				t.Logf("%s: allocations per frame %v = %v", mode, off, on)
			})
		})
	}
}
