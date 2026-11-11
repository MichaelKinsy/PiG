package runtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// apps/p2/src/runtime/refs.ts has no unit test upstream: the web and TUI tests (ui-reconnect, ui-resume, tui-attach,
// tui-p4) use it through their screens (lanes D and E). These cases pin follow, whenLive and Lingering against fakes.

// source is a NativeWatchable the test drives.
type source[T any] struct {
	mu       sync.Mutex
	value    T
	listener func(context.Context, chord.NativeWatchCall[T]) error
	watches  atomic.Int32
}

func (s *source[T]) Value() T {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

func (s *source[T]) Watch(ctx context.Context) (chord.NativeWatchHandle[T], error) {
	s.watches.Add(1)
	return &handle[T]{source: s, ctx: ctx, closed: make(chan struct{})}, nil
}

func (s *source[T]) set(value T) {
	s.mu.Lock()
	s.value = value
	listener := s.listener
	s.mu.Unlock()
	if listener != nil {
		_ = listener(context.Background(), chord.NativeWatchCall[T]{Kind: chord.WatchCallValue, Value: value})
	}
}

type handle[T any] struct {
	source *source[T]
	ctx    context.Context
	closed chan struct{}
}

func (h *handle[T]) Value() T { return h.source.Value() }
func (h *handle[T]) Start(listener func(context.Context, chord.NativeWatchCall[T]) error) {
	h.source.mu.Lock()
	h.source.listener = listener
	h.source.mu.Unlock()
}
func (h *handle[T]) Stop() (chord.WatchEnd, error) {
	return chord.WatchEnd{Kind: chord.WatchStopped}, nil
}
func (h *handle[T]) Closed() <-chan struct{} { return h.closed }
func (h *handle[T]) End() chord.WatchEnd     { return chord.WatchEnd{Kind: chord.WatchStopped} }

type recorder[T any] struct {
	mu  sync.Mutex
	got []T
}

func (r *recorder[T]) add(value T) {
	r.mu.Lock()
	r.got = append(r.got, value)
	r.mu.Unlock()
}

func (r *recorder[T]) values() []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]T(nil), r.got...)
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestFollowCallsWithTheValueAtOnceThenWithEachChange(t *testing.T) {
	src := &source[string]{value: "a"}
	var seen recorder[string]
	stop := Follow[string](src, seen.add)
	if got := seen.values(); len(got) < 1 || got[0] != "a" {
		t.Fatalf("first call = %v, want a at once", got)
	}
	eventually(t, "the watch", func() bool { src.mu.Lock(); defer src.mu.Unlock(); return src.listener != nil })
	src.set("b")
	src.set("c")
	if got := seen.values(); len(got) != 3 || got[1] != "b" || got[2] != "c" {
		t.Fatalf("calls = %v, want [a b c]: the watch's own value equals the first one and is not repeated", got)
	}
	stop()
	src.set("d")
	if got := seen.values(); len(got) != 3 {
		t.Fatalf("calls after stop = %v", got)
	}
}

type fakeRef struct {
	state    *source[*host.RefState]
	released atomic.Int32
}

func (f *fakeRef) State() chord.NativeWatchable[*host.RefState] { return f.state }
func (f *fakeRef) Release()                                     { f.released.Add(1) }

func TestWhenLive(t *testing.T) {
	ctx := context.Background()
	live := &host.RefState{Kind: host.RefStateLive}
	loading := &host.RefState{Kind: host.RefStateLoading}

	ref := &fakeRef{state: &source[*host.RefState]{value: live}}
	if err := WhenLive(ctx, ref); err != nil {
		t.Fatalf("already live: %v", err)
	}

	// A transient failure keeps waiting; going live resolves.
	ref = &fakeRef{state: &source[*host.RefState]{value: &host.RefState{Kind: host.RefStateFailed, Cause: data.FailureCause{Kind: data.FailureTransient}}}}
	result := make(chan error, 1)
	go func() { result <- WhenLive(ctx, ref) }()
	time.Sleep(20 * time.Millisecond)
	select {
	case err := <-result:
		t.Fatalf("a transient failure resolved: %v", err)
	default:
	}
	eventually(t, "the watch", func() bool { return ref.state.watches.Load() > 0 })
	ref.state.set(loading)
	ref.state.set(live)
	if err := <-result; err != nil {
		t.Fatalf("went live: %v", err)
	}

	// Broken and ended references reject with their error.
	boom := &data.ChordError{}
	for name, state := range map[string]*host.RefState{
		"broken": {Kind: host.RefStateFailed, Cause: data.FailureCause{Kind: data.FailureBroken, Error: boom}},
		"ended":  {Kind: host.RefStateEnded, Error: boom},
	} {
		ref = &fakeRef{state: &source[*host.RefState]{value: state}}
		if err := WhenLive(ctx, ref); !errors.Is(err, boom) {
			t.Errorf("%s: WhenLive = %v, want the reference's error", name, err)
		}
		if ErrorOf(ref) != error(boom) {
			t.Errorf("%s: ErrorOf = %v", name, ErrorOf(ref))
		}
	}

	// The context ends the wait.
	ref = &fakeRef{state: &source[*host.RefState]{value: loading}}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := WhenLive(cancelled, ref); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled WhenLive = %v", err)
	}
	if StatusOf(ref) != host.RefStateLoading || ErrorOf(ref) != nil {
		t.Errorf("StatusOf/ErrorOf of a loading reference: %v %v", StatusOf(ref), ErrorOf(ref))
	}
}

func TestLingeringReleasesLaterAndCapsTheWaiting(t *testing.T) {
	newRef := func() *fakeRef { return &fakeRef{state: &source[*host.RefState]{}} }
	l := NewLingering(map[string]time.Duration{"p2.chat": time.Hour}, 2)

	untyped := newRef()
	l.Release(untyped, "other")
	if untyped.released.Load() != 1 {
		t.Error("a type without a delay is released at once")
	}

	a, b, c := newRef(), newRef(), newRef()
	l.Release(a, "p2.chat")
	l.Release(b, "p2.chat")
	if a.released.Load() != 0 || b.released.Load() != 0 {
		t.Fatal("released before its delay")
	}
	l.Release(c, "p2.chat")
	if a.released.Load() != 1 || b.released.Load() != 0 || c.released.Load() != 0 {
		t.Fatalf("past the cap the oldest goes first: a=%d b=%d c=%d", a.released.Load(), b.released.Load(), c.released.Load())
	}
	l.Flush()
	if b.released.Load() != 1 || c.released.Load() != 1 {
		t.Fatal("Flush releases every waiting reference")
	}
	l.Flush()
	if a.released.Load() != 1 || b.released.Load() != 1 {
		t.Fatal("a reference is released once")
	}
}

func TestLingeringReleasesAfterItsDelay(t *testing.T) {
	l := NewLingering(map[string]time.Duration{"p2.chat": 10 * time.Millisecond}, 4)
	ref := &fakeRef{state: &source[*host.RefState]{}}
	l.Release(ref, "p2.chat")
	eventually(t, "the delayed release", func() bool { return ref.released.Load() == 1 })
}

// endedSource is a NativeWatchable whose watch has ended when follow gets it: the reference it follows was released between the
// watch's making and its start. started is closed when follow starts the watch.
type endedSource struct {
	value   int
	started chan struct{}
}

func (s endedSource) Value() int { return s.value }
func (s endedSource) Watch(context.Context) (chord.NativeWatchHandle[int], error) {
	watch := chord.NewNativeWatch(s.value, func() {}, func(error) {}, nil)
	_, _ = watch.Stop()
	return startNotice{NativeWatchHandle: watch, started: s.started}, nil
}

type startNotice struct {
	chord.NativeWatchHandle[int]
	started chan struct{}
}

func (n startNotice) Start(listener func(context.Context, chord.NativeWatchCall[int]) error) {
	close(n.started)
	n.NativeWatchHandle.Start(listener)
}

// A watch that ended before follow started it (Start panics `Watch is stopped`, as Pi's throws) has nothing to follow: follow
// goes on to its stop, where it used to panic on the goroutine of its watch, which took the process with it.
func TestFollowOfAWatchThatEndedBeforeItsStartHasNothingToFollow(t *testing.T) {
	var seen []int
	var mu sync.Mutex
	started := make(chan struct{})
	stop := Follow[int](endedSource{value: 7, started: started}, func(value int) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, value)
	})
	<-started
	stop()
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 || seen[0] != 7 {
		t.Fatalf("seen = %v, want the source's value first", seen)
	}
}

// endingSource is a NativeWatchable whose reference ends after follow made its watch and before follow started it: the watch
// holds the value it was made at, the source's value is the ended one, and the watch is stopped when follow starts it.
type endingSource struct {
	mu      sync.Mutex
	value   int
	started chan struct{}
}

func (s *endingSource) Value() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

func (s *endingSource) Watch(context.Context) (chord.NativeWatchHandle[int], error) {
	s.mu.Lock()
	made := s.value
	// The reference ends: its state is the ended one and its watches stop.
	s.value = made + 1
	s.mu.Unlock()
	watch := chord.NewNativeWatch(made, func() {}, func(error) {}, nil)
	_, _ = watch.Stop()
	return startNotice{NativeWatchHandle: watch, started: s.started}, nil
}

// A reference that ends between follow's watch and its start is followed to its end: the ended state is the source's value
// then and no change will bring it, so follow hands it over (a held reference's FailureOf waited 20 s for it).
func TestFollowOfAWatchThatEndedBeforeItsStartDeliversTheValueItEndedWith(t *testing.T) {
	var seen []int
	var mu sync.Mutex
	started := make(chan struct{})
	stop := Follow[int](&endingSource{value: 7, started: started}, func(value int) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, value)
	})
	defer stop()
	<-started
	eventually(t, "the value the source ended with", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == 2 && seen[0] == 7 && seen[1] == 8
	})
}

// Pi main 8b02b459: keptIn returns a reference still bound in a variant while it reconnects; liveIn drops it.
func TestKeepsOutlastsADroppedConnection(t *testing.T) {
	broken := &host.RefState{Kind: host.RefStateFailed, Cause: data.FailureCause{Kind: data.FailureBroken}}
	transient := &host.RefState{Kind: host.RefStateFailed, Cause: data.FailureCause{Kind: data.FailureTransient}}
	cases := []struct {
		name    string
		state   *host.RefState
		variant string
		want    bool
	}{
		{"loading in the variant", &host.RefState{Kind: host.RefStateLoading}, "owner", true},
		{"live in the variant", &host.RefState{Kind: host.RefStateLive}, "owner", true},
		{"failed transiently keeps its variant", transient, "owner", true},
		{"failed broken", broken, "owner", false},
		{"ended", &host.RefState{Kind: host.RefStateEnded}, "owner", false},
		{"bound in another variant", &host.RefState{Kind: host.RefStateLive}, "guest", false},
		{"never bound", &host.RefState{Kind: host.RefStateLoading}, "", false},
	}
	for _, c := range cases {
		if got := keeps(c.state, c.variant, []string{"owner"}); got != c.want {
			t.Errorf("%s: keeps = %v, want %v", c.name, got, c.want)
		}
	}
}
