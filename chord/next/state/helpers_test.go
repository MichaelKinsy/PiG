package state_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// The Go counterparts of the pieces of the TypeScript tests' `loaded()` host that the state package needs: a Batch, the
// delivery scheduler its watches run on, a microtask queue for Soon and the errors the batch reported. The upstream
// tests reach the batch through a ChordHost, which lives in another package.

var background = chordctx.Background()

// errorLog collects what a batch reported, as the host's onError does.
type errorLog struct {
	mu    sync.Mutex
	items []state.BatchError
}

func (log *errorLog) add(err state.BatchError) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.items = append(log.items, err)
}

func (log *errorLog) all() []state.BatchError {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]state.BatchError{}, log.items...)
}

// microtasks is the queue a host gives its batch for Soon; run drains it as the event loop does between turns.
type microtasks struct {
	mu   sync.Mutex
	jobs []func()
}

func (queue *microtasks) enqueue(job func()) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.jobs = append(queue.jobs, job)
}

func (queue *microtasks) pending() int {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return len(queue.jobs)
}

func (queue *microtasks) run() {
	for {
		queue.mu.Lock()
		if len(queue.jobs) == 0 {
			queue.mu.Unlock()
			return
		}
		job := queue.jobs[0]
		queue.jobs = queue.jobs[1:]
		queue.mu.Unlock()
		job()
	}
}

type harness struct {
	batch     *state.Batch
	scheduler *chord.DeliveryScheduler
	micro     *microtasks
	errors    *errorLog
	meter     telemetry.TelemetryMetrics
}

// newHarness returns a harness whose batch counts on metrics when it is not nil.
func newHarness(t *testing.T, metrics telemetry.TelemetryMetrics) *harness {
	t.Helper()
	h := &harness{scheduler: chord.NewDeliveryScheduler(), micro: &microtasks{}, errors: &errorLog{}, meter: metrics}
	h.batch = state.NewBatch(h.errors.add, metrics).UseScheduler(h.scheduler).UseMicrotasks(h.micro.enqueue)
	return h
}

// delivered waits until every queued watch frame has been delivered, support.ts `delivered()`.
func (h *harness) delivered() { h.scheduler.Wait() }

func ops(items ...delta.Op) []delta.Op {
	if items == nil {
		return []delta.Op{}
	}
	return items
}

func op(items ...any) delta.Op { return delta.Op(items) }

var errThrew = errors.New("t threw")

// docSource is a singleton document as the documents package makes one: a Source whose value changes inside a commit, and
// which publishes the ops from the value its watches had to the one it has when the batch delivers.
type docSource struct {
	state.Source
	value   delta.JsonValue
	base    delta.JsonValue
	changed bool
	retired bool
}

func newDoc(batch *state.Batch, value delta.JsonValue) *docSource {
	doc := &docSource{value: value}
	doc.Source.Init(batch, doc)
	return doc
}

func (doc *docSource) Value() (delta.JsonValue, error) { return doc.value, nil }

func (doc *docSource) Deliver(ctx context.Context) error {
	ops := delta.DiffRevisions(doc.base, doc.value)
	retired := doc.retired
	if len(ops) > 0 || retired {
		doc.Publish(doc.value, ops, retired, ctx)
	}
	return nil
}

// set changes the value inside a batch of its own, or the one running.
func (doc *docSource) set(value delta.JsonValue) error {
	return doc.batch().Within(func() error {
		doc.begin()
		doc.value = value
		return nil
	}, background)
}

// retire marks the document retired in the batch, and its value back to value (what the address shows after it).
func (doc *docSource) retire(value delta.JsonValue) error {
	return doc.batch().Within(func() error {
		doc.begin()
		doc.value = value
		doc.retired = true
		doc.RetiredInBatch()
		return nil
	}, background)
}

// begin records the first change of a batch: the value its watches have, and the end of the batch, where the record goes.
func (doc *docSource) begin() {
	if doc.changed {
		return
	}
	doc.changed, doc.base = true, doc.value
	doc.batch().Flushing(func(context.Context) error {
		doc.changed, doc.retired, doc.base = false, false, nil
		return nil
	})
	doc.Changed()
}

func (doc *docSource) batch() *state.Batch { return doc.Source.Batch() }

func num(n float64) *delta.JsonObject { return delta.JsonObjectOf("n", n) }

func nanValue() float64 { var zero float64; return zero / zero }
