package codingagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// paintProbe reports every render on frames.
type paintProbe struct{ frames chan struct{} }

func (p *paintProbe) Render(int) []string {
	select {
	case p.frames <- struct{}{}:
	default:
	}
	return []string{"probe"}
}

func (p *paintProbe) Invalidate() {}

// A throttled frame runs on the owner loop. Pi's requestRender arms one setTimeout on its event loop; here each frame
// arms a new time.AfterFunc, whose callback runs on a goroutine of its own and hops to the owner through
// dispatchScheduledRender and renderWakeCh. Keeping the next-frame deadline in the owner loop's select (one reusable
// timer the loop owns) renders the same frames with no goroutine per frame (TU-T11). traceHops counts the goroutines
// the measured frames start, not the runtime's own (the first collection in the window starts one GC worker per P, and
// the trace starts its readers), and the test runs in its own process so that no other test's goroutines start any
// (testenv.InOwnProcess).
func TestThrottledFramesStartNoGoroutinePerFrame(t *testing.T) {
	if !testenv.InOwnProcess(t) {
		return
	}
	m := statusBorderMode(t, false)
	probe := &paintProbe{frames: make(chan struct{}, 1)}
	m.tuiInst.Add(probe)
	m.installRenderDispatcher()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.backgroundCtx = ctx
	m.inputReadCh = make(chan inputChunk)
	m.inputErrCh = make(chan error, 1)
	until, done := make(chan struct{}), make(chan error, 1)
	go func() { done <- m.inputLoopUntil(ctx, strings.NewReader(""), until) }()
	defer func() {
		close(until)
		if err := <-done; err != nil {
			t.Errorf("input loop: %v", err)
		}
	}()

	frame := func() {
		if err := m.postToMain(ctx, func() { m.tuiInst.RequestRender() }); err != nil {
			t.Fatal(err)
		}
		select {
		case <-probe.frames:
		case <-time.After(5 * time.Second):
			t.Fatal("the requested frame never rendered")
		}
		time.Sleep(20 * time.Millisecond) // past the 16 ms frame throttle, as a streamed answer's frames are
	}
	frame()
	const frames = 50
	_, started := traceHops(t, func() {
		for range frames {
			frame()
		}
	})
	perFrame := float64(started) / frames
	t.Logf("%.2f goroutines started per frame", perFrame)
	if perFrame > 0.1 {
		t.Fatalf("each throttled frame starts %.2f goroutines, want none: the frame deadline is a new AfterFunc timer whose callback hops to the owner loop", perFrame)
	}
}
