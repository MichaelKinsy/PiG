package host

import (
	"context"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/host/ref-state.ts. The state of a reference (§7): loading, live, failed or ended; and
// `ref.state`, the native watchable that follows it.

// RefStateKind is where a reference stands.
type RefStateKind string

const (
	// RefStateLoading: its object activates, retries or is parked on dependencies still under way, it waits for its host's
	// publication or a parent link, or its link is down (the reference's coarser view of its object's waiting and
	// activating).
	RefStateLoading RefStateKind = "loading"
	// RefStateLive: bound.
	RefStateLive RefStateKind = "live"
	// RefStateFailed: its object failed and no attempt runs (`transient`: RetryAt is when its backoff retries it; `broken`:
	// no automatic retry), or nothing fits it or its object is parked on a failed dependency (`waiting`: an event wakes it,
	// RetryAt nil).
	RefStateFailed RefStateKind = "failed"
	// RefStateEnded: for good, with the error that says why (`released` without a reason: released by its holder;
	// `released/ended`: its object ended; `released/closed`: its host closed; `forbidden/revoked`).
	RefStateEnded RefStateKind = "ended"
)

// RefState is where a reference stands. It is used as a pointer and compared by identity: a change of state is a new
// object (a native watch compares by identity), and refLoading and refLive are the one object of their kind. A state is
// never changed once made. Go forced difference: Pi's discriminated union is one struct with the fields of every kind;
// only the fields of its Kind are set.
type RefState struct {
	Kind RefStateKind
	// Cause is why a failed reference failed.
	Cause data.FailureCause
	// RetryAt is when a failed reference is retried (Date.now() time, ms); nil is Pi's undefined.
	RetryAt *int64
	// Error is why an ended reference ended.
	Error *data.ChordError
}

// failed is a failed reference's state: Pi's `Extract<RefState, { kind: "failed" }>`, which Go cannot express.
type failed = RefState

// ended is an ended reference's state: Pi's `Extract<RefState, { kind: "ended" }>`.
type ended = RefState

// refLoading and refLive are the states without fields, one object each: a change of state is a new object (a native watch
// compares by identity). Pi's `LOADING` and `LIVE`.
var (
	refLoading = &RefState{Kind: RefStateLoading}
	refLive    = &RefState{Kind: RefStateLive}
)

// referenceState is `ref.state`, a native source: its value is the reference's state, read through `read`; each watch gets
// every new state, in its own context (not one call per batch, as a followed source's), and ends `ended` when the
// reference is released (the end is the notification). Derives reading it are marked in a batch of its own when it
// changes. It is used inside the host's sections; a watch ends itself from goroutines of its own, so its watches are
// locked.
type referenceState struct {
	state.NativeSource[*RefState]
	read      func() *RefState
	scheduler *chord.DeliveryScheduler
	mu        sync.Mutex
	// watches are the watches with the contexts they were taken in, which their frames carry, in the order taken.
	watches []referenceWatch
	// last is the state the watches have: a change is announced only when the state is another object.
	last *RefState
}

type referenceWatch struct {
	watch *chord.NativeWatch[*RefState]
	ctx   context.Context
}

var _ state.NativeHooks[*RefState] = (*referenceState)(nil)

// newReferenceState makes `ref.state` of a reference read through read. batch is the host's, nil for a reference that
// keeps no host (an ended one); scheduler is the batch's delivery scheduler for the watches (the host owns both; nil is the
// process default).
func newReferenceState(read func() *RefState, batch *state.Batch, scheduler *chord.DeliveryScheduler) *referenceState {
	r := &referenceState{read: read, scheduler: scheduler}
	r.Init(batch, r)
	r.last = read()
	return r
}

// Value is the reference's state.
func (r *referenceState) Value() *RefState { return r.read() }

// Watch is Pi's `watch(context)`, which resolves with a watch that gets every state in order: a watch sees each step it
// went through. It never suspends; the call returns the watch or the error Pi's promise rejects with.
func (r *referenceState) Watch(ctx context.Context) (chord.NativeWatchHandle[*RefState], error) {
	current := r.read()
	batch := r.Batch()
	// An ended reference has nothing more to announce, and keeps no host to report to.
	if batch == nil || current.Kind == RefStateEnded {
		over := chord.NewNativeWatch(current, func() {}, func(error) {}, nil)
		over.EndWith(chord.WatchEnd{Kind: chord.WatchEnded})
		return over, nil
	}
	if err := batch.AssertOutside("watch"); err != nil {
		return nil, err
	}
	var watch *chord.NativeWatch[*RefState]
	watch = chord.NewNativeWatch(current, func() { r.detach(watch) }, batch.ReportListener, nil)
	if r.scheduler != nil {
		watch.On(r.scheduler)
	}
	r.mu.Lock()
	r.watches = append(r.watches, referenceWatch{watch: watch, ctx: ctx})
	r.mu.Unlock()
	if ctx.Done() != nil {
		watch.ObserveAbort(ctx)
	}
	return watch, nil
}

// detach removes watch from the watches (it ended).
func (r *referenceState) detach(watch *chord.NativeWatch[*RefState]) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if at := slices.IndexFunc(r.watches, func(entry referenceWatch) bool { return entry.watch == watch }); at >= 0 {
		r.watches = slices.Delete(r.watches, at, at+1)
	}
}

// snapshot is the watches as they are now: ending a watch removes it from them.
func (r *referenceState) snapshot() []referenceWatch {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.watches)
}

// moved: the reference's state may have changed: every watch gets it when it is another state, derives are marked.
func (r *referenceState) moved() {
	current := r.read()
	if current == r.last {
		return
	}
	r.last = current
	for _, entry := range r.snapshot() {
		entry.watch.Advance(entry.ctx, current)
	}
	if r.DependentSet().Len() > 0 {
		r.mark()
	}
}

// released: the reference ended (`finished`, the reference's state): every watch ends `ended`, derives are marked a last time.
func (r *referenceState) released(finished *ended) {
	// An ended reference keeps nothing of its object: not the last failure's error, not the host.
	r.last = finished
	for _, entry := range r.snapshot() {
		entry.watch.EndWith(chord.WatchEnd{Kind: chord.WatchEnded})
	}
	batch := r.Batch()
	if batch == nil || r.DependentSet().Len() == 0 {
		r.End()
		return
	}
	// Ended where the marks run: a release while a batch's flushes send marks in the next commit, after this. The adoption
	// returns no error, and Within returns only an adoption's.
	_ = batch.Within(func() error {
		r.Changed()
		r.End()
		return nil
	}, chordctx.Background())
}

// mark marks the derives reading it, in the batch running now or one of its own (apart from `moved`: a closure there would
// allocate on every state change). A reference has no context of its own: the background context.
func (r *referenceState) mark() {
	if batch := r.Batch(); batch != nil {
		_ = batch.Within(func() error {
			r.Changed()
			return nil
		}, chordctx.Background())
	}
}
