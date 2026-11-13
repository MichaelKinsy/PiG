package host

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Own tests: Pi's openWatch (packages/chord/src/state/native-source.ts:132) reads the source, records what its followers
// have and registers the watch in one synchronous turn of the one event loop. A Go caller outside the host's loop (a
// native's open, the goroutine of a request) took the read as a section of its own and registered after it: a commit
// between the two changed a source nobody followed yet, which lists no frame, and the follower stayed at the older value
// for good (a session worker whose Harness waited for the models plugin's configuration never went live).

// gapSource is a followed source whose Start, once armed, lets a commit land right after its value is read: where the
// read of a registry's value from outside the loop ends its section.
type gapSource struct {
	state.FollowedSource[any]
	mu        sync.Mutex
	value     any
	afterRead func()
}

func (s *gapSource) Value() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

func (s *gapSource) Start() state.NativeStart[any] {
	value := s.Value()
	s.mu.Lock()
	hook := s.afterRead
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	return state.NativeStart[any]{Kind: state.StartValue, Value: value}
}

func (s *gapSource) set(value any) {
	s.mu.Lock()
	s.value = value
	s.mu.Unlock()
}

// gapObserver records the calls of a synchronous observer.
type gapObserver struct {
	mu    sync.Mutex
	calls []any
}

func (o *gapObserver) Advance(call chord.NativeWatchCall[any], _ context.Context) {
	o.mu.Lock()
	o.calls = append(o.calls, call.Value)
	o.mu.Unlock()
}

func (o *gapObserver) End(chord.WatchEnd) {}

func (o *gapObserver) seen() []any {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]any{}, o.calls...)
}

// gapHost is a followed source at "old" on a batch wired as a host's: its sections are a Loop's.
type gapHost struct {
	source *gapSource
	loop   *Loop
	batch  *state.Batch
}

func newGapHost(t *testing.T) *gapHost {
	t.Helper()
	loop := NewLoop()
	scheduler := chord.NewDeliveryScheduler()
	batch := state.NewBatch(func(state.BatchError) {}, nil).UseScheduler(scheduler).UseMicrotasks(loop.Queue).UseSection(loop.Run, loop.InSection)
	t.Cleanup(func() {
		scheduler.Wait()
		loop.Wait()
	})
	source := &gapSource{value: "old"}
	source.Init(batch, source)
	return &gapHost{source: source, loop: loop, batch: batch}
}

// commit commits value as a section of the loop.
func (h *gapHost) commit(t *testing.T, value any) {
	t.Helper()
	h.loop.Run(func() {
		if err := h.batch.Commit(func() error {
			h.source.set(value)
			h.source.Changed()
			return nil
		}, memberBackground); err != nil {
			t.Errorf("commit: %v", err)
		}
	})
}

// landing arms the gap: once the next Start has read "old", another goroutine commits "new". That Start goes on when the
// commit is done, or after 300 ms when the commit cannot run before the follower is registered (the read and the
// registration are one section). committed waits for the commit.
func (h *gapHost) landing(t *testing.T) (committed func()) {
	t.Helper()
	read, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.source.mu.Lock()
	h.source.afterRead = func() {
		once.Do(func() {
			close(read)
			select {
			case <-done:
			case <-time.After(300 * time.Millisecond):
			}
		})
	}
	h.source.mu.Unlock()
	var committing sync.WaitGroup
	committing.Go(func() {
		<-read
		h.commit(t, "new")
		close(done)
	})
	return committing.Wait
}

func TestAWatchOpenedFromOutsideTheLoopIsToldOfAChangeCommittedWhileItOpened(t *testing.T) {
	h := newGapHost(t)
	committed := h.landing(t)
	watch, err := h.source.Watch(memberBackground)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	defer func() { _, _ = watch.Stop() }()
	values := make(chan any, 4)
	watch.Start(func(_ context.Context, call chord.NativeWatchCall[any]) error {
		if call.Kind == chord.WatchCallValue {
			values <- call.Value
		}
		return nil
	})
	committed()
	if watch.Value() == "new" {
		return
	}
	select {
	case value := <-values:
		expectEqual(t, "the watch's call", value, any("new"))
	case <-time.After(3 * time.Second):
		t.Fatalf("the watch opened at %v and was never told of the value committed while it opened", watch.Value())
	}
}

func TestAnObserverAttachedFromOutsideTheLoopIsToldOfAChangeCommittedWhileItAttached(t *testing.T) {
	h := newGapHost(t)
	committed := h.landing(t)
	observer := &gapObserver{}
	observation, err := h.source.Observe(observer, memberBackground)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	defer observation.Detach()
	committed()
	if observation.Value == "new" {
		return
	}
	// An observer is advanced inside the batch that changed its source: the commit is done, so the call came or never will.
	expectEqual(t, "the observer's calls", observer.seen(), []any{"new"})
}

func TestASubscriberAddedFromOutsideTheLoopIsToldOfAChangeCommittedWhileItWasAdded(t *testing.T) {
	h := newGapHost(t)
	committed := h.landing(t)
	observer := &gapObserver{}
	h.source.Subscribe(observer)
	defer h.source.Unsubscribe(observer)
	committed()
	// A subscriber is fed every call from its subscription on, inside each batch.
	expectEqual(t, "the subscriber's calls", observer.seen(), []any{"new"})
}

// A caller in a section (a factory, a method handler) opens its watch inline, without waiting for the loop it holds; a
// watch opened once the source ended is ended, from outside the loop too.
func TestAWatchOpenedInsideASectionOpensInlineAndOneOpenedAfterTheSourceEndedIsEnded(t *testing.T) {
	h := newGapHost(t)
	var watch chord.NativeWatchHandle[any]
	var err error
	opened := make(chan struct{})
	go func() {
		defer close(opened)
		h.loop.Run(func() { watch, err = h.source.Watch(memberBackground) })
	}()
	select {
	case <-opened:
	case <-time.After(10 * time.Second):
		t.Fatal("a watch opened inside a section waits for the loop its caller holds")
	}
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	values := make(chan any, 4)
	watch.Start(func(_ context.Context, call chord.NativeWatchCall[any]) error {
		if call.Kind == chord.WatchCallValue {
			values <- call.Value
		}
		return nil
	})
	h.commit(t, "new")
	select {
	case value := <-values:
		expectEqual(t, "the watch's call", value, any("new"))
	case <-time.After(3 * time.Second):
		t.Fatal("the watch opened inside a section was not told of a later commit")
	}
	h.loop.Run(h.source.End)
	late, err := h.source.Watch(memberBackground)
	if err != nil {
		t.Fatalf("watch of the ended source: %v", err)
	}
	select {
	case <-late.Closed():
	case <-time.After(10 * time.Second):
		t.Fatal("a watch opened after the source ended did not end")
	}
	expectEqual(t, "its end", late.End(), chord.WatchEnd{Kind: chord.WatchEnded})
}
