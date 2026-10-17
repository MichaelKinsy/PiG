package tui

import (
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuilder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuilder) take() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.b.String()
	l.b.Reset()
	return s
}

// Pi: packages/tui/src/tui.ts:990 requestRender(force) and packages/tui/test/tui-render.test.ts (`tui.requestRender(true)` then waitForRender):
// a forced request resets the differential render state, so the next frame repaints every line, while a plain request writes only the lines
// that changed.
func TestRequestRenderForceRepaintsEveryLine(t *testing.T) {
	frame := func(t *testing.T, force bool) string {
		t.Helper()
		out := &lockedBuilder{}
		ui := NewWithOutput(out, 40, 10)
		component := &fixedLinesComponent{lines: []string{"stable line", "changing line 1"}}
		ui.Add(component)
		ui.Start()
		defer ui.Stop()
		ui.Render()
		out.take()
		component.lines = []string{"stable line", "changing line 2"}
		if force {
			ui.RequestRender(true)
		} else {
			ui.RequestRender()
		}
		deadline := time.Now().Add(2 * time.Second)
		var got string
		for time.Now().Before(deadline) {
			got += out.take()
			if strings.Contains(got, "changing line 2") {
				return got
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("force=%v: no frame written: %q", force, got)
		return ""
	}
	if got := frame(t, false); strings.Contains(got, "stable line") {
		t.Errorf("a plain RequestRender rewrote the unchanged line: %q", got)
	}
	if got := frame(t, true); !strings.Contains(got, "stable line") {
		t.Errorf("RequestRender(true) did not repaint the unchanged line: %q", got)
	}
}
