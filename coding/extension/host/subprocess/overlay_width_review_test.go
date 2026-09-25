package subprocess

import "testing"

type widthOverlayHandle struct {
	*testOverlayHandle
	width int
}

func (h *widthOverlayHandle) UpdateLinesAt(lines []string, width int) {
	h.width = width
	h.UpdateLines(lines)
}

func TestOverlayProxyPreservesTerminalWidth(t *testing.T) {
	for _, buffered := range []bool{false, true} {
		proxy := newOverlayProxy()
		handle := &widthOverlayHandle{testOverlayHandle: newTestOverlayHandle()}
		if !buffered {
			proxy.SetTarget(handle)
		}
		proxy.UpdateFrame([]string{"frame"}, 120, 1, 120)
		if buffered {
			proxy.SetTarget(handle)
		}
		if handle.width != 120 {
			t.Fatalf("buffered=%v: frame width=%d, want 120", buffered, handle.width)
		}
	}
}
