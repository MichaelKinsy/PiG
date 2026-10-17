package codingagent

import (
	"bytes"
	"errors"
	"io"
	"maps"
	"runtime/trace"
	"slices"
	"strings"
	"testing"

	xtrace "golang.org/x/exp/trace"
)

// pigFramePrefix starts the function name of every frame this module owns.
const pigFramePrefix = "github.com/MichaelKinsy/PiG/"

// traceHops runs fn under runtime/trace and counts what the code under test does while it runs: the scheduler hand-offs
// it makes and the goroutines it starts. A hand-off is a goroutine blocked in this module's code (a channel, a mutex, a
// condition) that another goroutine unblocks. Every hand-off can wake a P.
//
// The trace sees the whole process, so the count keeps only goroutines that belong to the code under test. It leaves out
// goroutines that never block in this module's code (the runtime's GC workers, sweeper and scavenger, and the trace's
// own readers, which wake on a clock of their own and, when a collection starts in the window, number as many as there
// are Ps), wake-ups from a timer or the network poller (a goroutine's own deadline is not a hand-off), and the
// goroutine that calls fn (the test's own waits). A goroutine started in the window counts unless its entry function
// is the runtime's or the trace's own.
func traceHops(t *testing.T, fn func()) (handoffs, started int) {
	t.Helper()
	var buf bytes.Buffer
	if err := trace.Start(&buf); err != nil {
		t.Skipf("runtime/trace unavailable: %v", err)
	}
	const region = "traceHops"
	trace.WithRegion(t.Context(), region, fn)
	trace.Stop()
	reader, err := xtrace.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	var (
		testG    = xtrace.NoGoroutine
		owned    = map[xtrace.GoID]bool{} // blocked in this module's code at some point in the window
		unblocks []xtrace.GoID            // goroutines another goroutine unblocked
	)
	for {
		event, err := reader.ReadEvent()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch event.Kind() {
		case xtrace.EventRegionBegin:
			if event.Region().Type == region {
				testG = event.Goroutine()
			}
			continue
		case xtrace.EventStateTransition:
		default:
			continue
		}
		transition := event.StateTransition()
		if transition.Resource.Kind != xtrace.ResourceGoroutine {
			continue
		}
		id := transition.Resource.Goroutine()
		from, to := transition.Goroutine()
		switch {
		case from == xtrace.GoRunning && to == xtrace.GoWaiting:
			for frame := range transition.Stack.Frames() {
				if strings.HasPrefix(frame.Func, pigFramePrefix) {
					owned[id] = true
					break
				}
			}
		case from == xtrace.GoWaiting && to == xtrace.GoRunnable:
			if event.Goroutine() != xtrace.NoGoroutine {
				unblocks = append(unblocks, id)
			}
		case from == xtrace.GoNotExist && to == xtrace.GoRunnable:
			// The start stack of a created goroutine is its entry function (Event.Stack would be the creator's).
			for frame := range transition.Stack.Frames() {
				if !strings.HasPrefix(frame.Func, "runtime.") && !strings.HasPrefix(frame.Func, "runtime/trace.") {
					started++
				}
				break
			}
		}
	}
	if testG == xtrace.NoGoroutine {
		t.Fatal("the trace has no region for the measured function")
	}
	// A filter that matches nothing counts nothing and passes any regression: the code under test must show.
	if !slices.ContainsFunc(slices.Collect(maps.Keys(owned)), func(id xtrace.GoID) bool { return id != testG }) {
		t.Fatal("no goroutine other than the test's blocked in this module's code during the window: the trace filter matches nothing")
	}
	for _, id := range unblocks {
		if id != testG && owned[id] {
			handoffs++
		}
	}
	return handoffs, started
}
