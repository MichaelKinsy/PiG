//go:build parity

package runner

import (
	"context"
	"strings"
	"sync"
	"time"
)

// frameWatchPoll is the pause between two samples of the visible pane. A sample costs one tmux capture, so the watch sees a frame that
// stays on screen for a few tens of milliseconds. A forbidden pane counts only when the next sample shows the same pane: a frame the
// program is still writing, which a capture can catch half drawn, does not stay.
const frameWatchPoll = 10 * time.Millisecond

// frameWatchSampleLimit is how many stale panes a result keeps for the failure message.
const frameWatchSampleLimit = 3

// FrameWatchResult is what a step's frame_watch saw while the step ran.
type FrameWatchResult struct {
	// Watched reports that a step carried a frame_watch.
	Watched bool
	// Samples is how many panes the watch captured.
	Samples int
	// MinSamples is the smallest Samples that shows the step lasted long enough to observe a frame between its keys and its end.
	MinSamples int
	// StaleCount is how many samples repeated the sample before them and were forbidden: a frame that stayed on screen.
	StaleCount int
	// Stale holds the first stale panes.
	Stale []string
}

// stale reports whether pane is a frame the watch forbids: it shows every StaleContains row and none of StaleUnlessContains.
func (w TmuxFrameWatch) stale(pane string) bool {
	for _, want := range w.StaleContains {
		if !strings.Contains(pane, want) {
			return false
		}
	}
	for _, unless := range w.StaleUnlessContains {
		if strings.Contains(pane, unless) {
			return false
		}
	}
	return len(w.StaleContains) > 0
}

// frameWatch samples the visible pane until finish.
type frameWatch struct {
	spec   TmuxFrameWatch
	stop   chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	result FrameWatchResult
	last   string
}

// startFrameWatch samples the pane of session in the background. Start it before the step's keys are sent, so a frame the keys cause is
// sampled from the first one.
func startFrameWatch(ctx context.Context, session string, spec TmuxFrameWatch) *frameWatch {
	w := &frameWatch{spec: spec, stop: make(chan struct{}), done: make(chan struct{})}
	w.result.Watched = true
	w.result.MinSamples = spec.MinSamples
	go func() {
		defer close(w.done)
		for {
			select {
			case <-w.stop:
				return
			case <-ctx.Done():
				return
			default:
			}
			w.observe(capturePaneVisible(ctx, session))
			select {
			case <-w.stop:
				return
			case <-ctx.Done():
				return
			case <-time.After(frameWatchPoll):
			}
		}
	}()
	return w
}

func (w *frameWatch) observe(pane string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.result.Samples++
	repeated := w.result.Samples > 1 && pane == w.last
	w.last = pane
	if !repeated || !w.spec.stale(pane) {
		return
	}
	w.result.StaleCount++
	if len(w.result.Stale) < frameWatchSampleLimit {
		w.result.Stale = append(w.result.Stale, pane)
	}
}

// finish stops the watch and returns what it saw.
func (w *frameWatch) finish() FrameWatchResult {
	close(w.stop)
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.result
}

// merge adds the watch of a later step to r.
func (r FrameWatchResult) merge(next FrameWatchResult) FrameWatchResult {
	r.Watched = r.Watched || next.Watched
	r.Samples += next.Samples
	r.MinSamples += next.MinSamples
	r.StaleCount += next.StaleCount
	for _, pane := range next.Stale {
		if len(r.Stale) < frameWatchSampleLimit {
			r.Stale = append(r.Stale, pane)
		}
	}
	return r
}
