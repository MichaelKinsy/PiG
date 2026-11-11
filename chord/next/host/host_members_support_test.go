package host

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Test support of the member files (follow, member-source, keyed-members, members): the Go counterparts of the pieces of
// the TypeScript tests' `loaded()` host that these files need. The upstream tests reach them through a ChordHost,
// which is a later lane's: a Batch on a Loop (its microtask queue is the loop's), the delivery scheduler its watches run
// on, and the errors it reported.

var memberBackground = chordctx.Background()

// memberHarness is a host's loop and batch, as a ChordHost wires them.
type memberHarness struct {
	loop      *Loop
	scheduler *chord.DeliveryScheduler
	batch     *state.Batch

	mu     sync.Mutex
	errors []state.BatchError
}

func newMemberHarness(t *testing.T) *memberHarness {
	t.Helper()
	h := &memberHarness{loop: NewLoop(), scheduler: chord.NewDeliveryScheduler()}
	h.batch = state.NewBatch(h.report, nil).UseScheduler(h.scheduler).UseMicrotasks(h.loop.Queue)
	t.Cleanup(h.loop.Wait)
	return h
}

func (h *memberHarness) report(err state.BatchError) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.errors = append(h.errors, err)
}

// reported is what the batch reported, in order.
func (h *memberHarness) reported() []state.BatchError {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]state.BatchError{}, h.errors...)
}

// delivered waits until every queued watch frame has been delivered: support.ts `delivered()`.
func (h *memberHarness) delivered() { h.scheduler.Wait() }

// commit runs adopt as a commit of the batch, in a section of the loop (support.ts `host.batch.commit`).
func (h *memberHarness) commit(t *testing.T, adopt func()) {
	t.Helper()
	h.loop.Run(func() {
		if err := h.batch.Commit(func() error { adopt(); return nil }, memberBackground); err != nil {
			t.Errorf("commit: %v", err)
		}
	})
}

// within runs adopt inside the running batch, or as a commit of its own, in a section of the loop.
func (h *memberHarness) within(t *testing.T, adopt func()) {
	t.Helper()
	h.loop.Run(func() {
		if err := h.batch.Within(func() error { adopt(); return nil }, memberBackground); err != nil {
			t.Errorf("within: %v", err)
		}
	})
}

// memberDoc is a singleton document as the documents package makes one: a Source whose value changes inside a commit,
// and which publishes the ops from the value its watches had to the one it has when the batch delivers.
type memberDoc struct {
	state.Source
	value   delta.JsonValue
	base    delta.JsonValue
	changed bool
}

func newMemberDoc(batch *state.Batch, value delta.JsonValue) *memberDoc {
	doc := &memberDoc{value: value}
	doc.Init(batch, doc)
	return doc
}

func (d *memberDoc) Value() (delta.JsonValue, error) { return d.value, nil }

func (d *memberDoc) Deliver(ctx context.Context) error {
	if ops := delta.DiffRevisions(d.base, d.value); len(ops) > 0 {
		d.Publish(d.value, ops, false, ctx)
	}
	return nil
}

// set changes the value inside the running batch, or one of its own (the caller is in a section).
func (d *memberDoc) set(t *testing.T, value delta.JsonValue) {
	t.Helper()
	err := d.Batch().Within(func() error {
		if !d.changed {
			d.changed, d.base = true, d.value
			d.Batch().Flushing(func(context.Context) error {
				d.changed, d.base = false, nil
				return nil
			})
			d.Changed()
		}
		d.value = value
		return nil
	}, memberBackground)
	if err != nil {
		t.Errorf("set: %v", err)
	}
}

// memberCount is the value {n: n} of the tests.
func memberCount(n float64) *delta.JsonObject { return delta.JsonObjectOf("n", n) }
