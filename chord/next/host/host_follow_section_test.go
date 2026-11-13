package host

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Own tests: FollowSection ports no Pi case. Pi's runtime reads its state and advances its observers in one synchronous
// turn of the one event loop (apps/p2/src/runtime/chord.ts:244-248 is such a runtime), so its advances come in the order of
// its reads. A Go runtime tells of its changes from goroutines of its own: Section is that one turn.

// sectionSource is an attached followed source at {n: 0}, its observer and the observer's Section.
func sectionSource(t *testing.T, h *memberHarness, label string) (*followSource, FollowObserver[any], FollowSection) {
	t.Helper()
	var observer FollowObserver[any]
	source := newFollowSource(
		func(attached FollowObserver[any], _ context.Context) (*FollowAttachment[delta.JsonValue], error) {
			observer = attached
			return &FollowAttachment[delta.JsonValue]{Value: memberCount(0), Detach: func() {}}, nil
		},
		followOwner{label: label, ctx: memberBackground, ended: func(chord.WatchEnd) {}},
		h.batch, h.loop,
	)
	if err := source.attach(memberBackground); err != nil {
		t.Fatalf("attach: %v", err)
	}
	section, ok := observer.(FollowSection)
	if !ok {
		t.Fatal("the observer of a followed source has no Section")
	}
	return source, observer, section
}

func TestFollowSectionRunsWorkOnceAsOneSectionOfTheLoopBeforeItReturnsAndInlineInsideOne(t *testing.T) {
	h := newMemberHarness(t)
	source, observer, section := sectionSource(t, h, "section")
	// From outside the loop. What work may do is the same section: enter the loop itself, a Section in a Section, the advance.
	ran, inLoop := 0, false
	section.Section(func() {
		ran++
		inLoop = h.loop.InSection()
		h.loop.Run(func() { ran += 10 })
		section.Section(func() { ran += 100 })
		observer.Advance(valueCall(memberCount(1), false, setOp(1)), memberBackground)
	})
	expectEqual(t, "ran", ran, 111)
	expectEqual(t, "in a section", inLoop, true)
	expectEqual(t, "value", h.read(t, source), delta.JsonValue(memberCount(1)))
	// From inside a section (a method handler telling of its own change): inline, without waiting for the loop its caller holds.
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.loop.Run(func() {
			section.Section(func() {
				ran += 1000
				observer.Advance(valueCall(memberCount(2), false, setOp(2)), memberBackground)
			})
		})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a Section inside a section waits for the loop its caller holds")
	}
	expectEqual(t, "ran", ran, 1111)
	expectEqual(t, "value", h.read(t, source), delta.JsonValue(memberCount(2)))
}

// Two goroutines tell of two changes at once: the first read its (older) value and is held before its advance. Its
// Section holds the loop, so the second's read and advance come after it, and the source ends at the newer value.
func TestFollowSectionKeepsTheReadsAndTheAdvancesOfTwoGoroutinesInOneOrder(t *testing.T) {
	h := newMemberHarness(t)
	source, observer, section := sectionSource(t, h, "two tellers")
	var mu sync.Mutex
	current := float64(0)
	set := func(n float64) {
		mu.Lock()
		current = n
		mu.Unlock()
	}
	read := func() float64 {
		mu.Lock()
		defer mu.Unlock()
		return current
	}
	firstRead, secondTold := make(chan struct{}), make(chan struct{})
	var told sync.WaitGroup
	told.Go(func() {
		set(1)
		section.Section(func() {
			n := read()
			close(firstRead)
			// The second telling ran to its end meanwhile (a Section that is not one), or cannot before this one ends.
			select {
			case <-secondTold:
			case <-time.After(300 * time.Millisecond):
			}
			observer.Advance(valueCall(memberCount(n), false, setOp(n)), memberBackground)
		})
	})
	<-firstRead
	told.Go(func() {
		set(2)
		section.Section(func() {
			n := read()
			observer.Advance(valueCall(memberCount(n), false, setOp(n)), memberBackground)
		})
		close(secondTold)
	})
	told.Wait()
	expectEqual(t, "value", h.read(t, source), delta.JsonValue(memberCount(2)))
}

// After the source's attempt ended (the host closed, say) the loop still takes sections: work runs once, as before, and
// the advance in it is dropped, as an Advance after the end is.
func TestFollowSectionAfterItsSourceDetachedStillRunsWorkOnceAndTheAdvanceInItIsDropped(t *testing.T) {
	h := newMemberHarness(t)
	source, observer, section := sectionSource(t, h, "detached")
	h.loop.Run(source.detach)
	ran := 0
	section.Section(func() {
		ran++
		observer.Advance(valueCall(memberCount(9), false, setOp(9)), memberBackground)
	})
	expectEqual(t, "ran", ran, 1)
	var kept delta.JsonValue
	h.loop.Run(func() { kept = source.value })
	expectEqual(t, "value", kept, delta.JsonValue(memberCount(0)))
}
