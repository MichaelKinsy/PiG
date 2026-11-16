package host

import (
	"errors"
	"sync/atomic"
	"testing"
)

// Pi list.ts:57-61 (`read`) and #rebound: `if (this.detached) return Promise.resolve(); this.#next?.close(); ... this.#next =
// tiling`, and the check of which tiling is pending or current with the `void this.read(...)` it leads to, are each one turn of Pi's one
// thread. A Go section is a critical region of its own: a decision made before the section that acts is stale when the
// section is entered. These cases hold the section's entry (the host's loop is wrapped, as the store's cases wrap the batch's
// section) and let the other side land first.

// seamedList is the live list of a screen with rows, and a way to hold the next section a goroutine enters.
type seamedList struct {
	l       *list
	entered chan struct{}
	release chan struct{}
	held    atomic.Bool
	after   atomic.Pointer[func()]
}

func newSeamedList(t *testing.T) (*seamedList, *lsWorld) {
	t.Helper()
	probe := &lsProbe{}
	w := lsHosted(t, 5, 3, probe, nil)
	t.Cleanup(w.close)
	lsSettled(w.host)
	h := w.host.(*chordHost)
	s := &seamedList{l: probe.lists.at(0).Value(), entered: make(chan struct{}), release: make(chan struct{})}
	events := h.events
	h.batch.UseSection(func(work func()) {
		if s.held.Load() && s.held.CompareAndSwap(true, false) {
			close(s.entered)
			<-s.release
		}
		events.Run(work)
		if after := s.after.Swap(nil); after != nil {
			(*after)()
		}
	}, onLoopOf(events))
	return s, w
}

func (s *seamedList) hold() { s.held.Store(true) }

func TestListRebindsNothingWhenANewerReadTookThePendingPlaceWhileItWaitedForItsSection(t *testing.T) {
	s, w := newSeamedList(t)
	l := s.l
	l.mu.Lock()
	current := l.current
	l.mu.Unlock()
	if current == nil {
		t.Fatal("the list has no current tiling")
	}
	s.hold()
	done := make(chan struct{})
	go func() { defer close(done); l.rebound(current) }()
	<-s.entered
	// A read for new arguments runs while the rebinding waits for its section: its tiling is the pending one.
	newer := make(chan func())
	go func() { newer <- l.Open(current.target, current.context()) }()
	load := <-newer
	l.mu.Lock()
	pending := l.next
	l.mu.Unlock()
	if pending == nil || pending == current {
		t.Fatalf("the newer read left pending = %v", pending)
	}
	close(s.release)
	<-done
	l.mu.Lock()
	next, shown := l.next, l.current
	l.mu.Unlock()
	if next != pending && shown != pending {
		t.Fatalf("the older target's rebinding replaced the newer read: next = %p, current = %p, the newer read = %p", next, shown, pending)
	}
	load()
	lsSettled(w.host)
}

func TestListOpenInstallsNoTilingOnAListDetachedWhileItsCheckWaitedForTheSection(t *testing.T) {
	s, w := newSeamedList(t)
	l := s.l
	l.mu.Lock()
	current := l.current
	l.mu.Unlock()
	detach := func() { l.Detach(errors.New("detached")) }
	s.after.Store(&detach)
	load := l.Open(current.target, current.context())
	l.mu.Lock()
	next := l.next
	l.mu.Unlock()
	if next != nil {
		t.Fatalf("a list detached right after its read opened holds a pending tiling: %p", next)
	}
	load()
	lsSettled(w.host)
}
