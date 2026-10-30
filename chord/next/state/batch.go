package state

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/next/state/batch.ts (Batch, BatchError, Delivered). The batch (W1), one per host: a commit
// adopts inside it, then every watched value that changed gets one frame, then the flushes it collected run (a link
// sends one message); Chord calls that would reenter it are refused while it runs.

// BatchErrorKind is what a batch reports: BatchListenerError, a watch listener that failed, or BatchDeliveryFailed, a
// frame delivery or flush that failed (a derive's value is no JSON).
type BatchErrorKind string

const (
	// BatchListenerError is a watch listener that failed with Error.
	BatchListenerError BatchErrorKind = "listener_error"
	// BatchDeliveryFailed is a frame delivery or a flush that failed with Error.
	BatchDeliveryFailed BatchErrorKind = "delivery_failed"
)

// BatchError is what a batch reports to the host's report function.
type BatchError struct {
	Kind  BatchErrorKind
	Error error
}

// BatchCounter names a unit of derive or registry work a batch counts on the host's metrics.
type BatchCounter string

const (
	CounterDeriveComputes   BatchCounter = "chord.derive.computes"
	CounterDeriveDiffs      BatchCounter = "chord.derive.diffs"
	CounterRegistryComposes BatchCounter = "chord.registry.composes"
	CounterRegistryAdapts   BatchCounter = "chord.registry.adapts"
)

// Delivered is what a commit lists to get its frame delivered: a watched source it changed (JSON or native).
type Delivered interface {
	// Deliver delivers this batch's frame to its watches and subscribers; called once per batch that listed it. An error
	// is reported as BatchDeliveryFailed and the other frames are still delivered.
	Deliver(ctx context.Context) error
}

// Batch is a host's batch: the reentrancy flag, the watched sources a commit changed, and where background errors go.
// Like Pi's Batch it has no locks: the host serializes every call (see the package documentation).
type Batch struct {
	report  func(BatchError)
	metrics telemetry.TelemetryMetrics
	// scheduler runs the deliveries of the watches the batch's sources open, when the host gave it one.
	scheduler *chord.DeliveryScheduler
	// microtasks queues a job to run after the current step of the host, which Soon needs.
	microtasks func(job func())
	// section runs work as a synchronous section of the host (nil: inline, the caller's own).
	section func(work func())
	// running is set while adopting, delivering or computing a derive; one flag per host.
	running bool
	// computing is set while a pure compute runs (a derive's, a registry's compose): nothing may change a source then.
	computing bool
	// changed is the watched sources the current commit changed, each listed once by its Changed.
	changed []Delivered
	// flushes runs once the current commit's frames were delivered, each listed once by Flushing.
	flushes []func(ctx context.Context) error
	// sending is set while the current commit's flushes run (its links send).
	sending bool
	// later is the changes Within deferred while the flushes sent, for the next commit.
	later []func() error
	// soon is the changes Soon collected for the commit on the next microtask, while one is queued.
	soon       []func()
	soonQueued bool
}

// NewBatch returns a batch reporting to report; metrics, when not nil, is where Count counts.
func NewBatch(report func(BatchError), metrics telemetry.TelemetryMetrics) *Batch {
	return &Batch{report: report, metrics: metrics}
}

// UseScheduler makes the watches of the batch's sources deliver on scheduler, the host's microtask queue for watches. It
// is called once, right after NewBatch, and returns the batch.
func (b *Batch) UseScheduler(scheduler *chord.DeliveryScheduler) *Batch {
	b.scheduler = scheduler
	return b
}

// UseMicrotasks gives the batch the queue Soon runs its commit on: queue runs job once after the current step of the
// host, under the host's serialization, as queueMicrotask runs it after the current synchronous code. It is called once,
// right after NewBatch, and returns the batch.
func (b *Batch) UseMicrotasks(queue func(job func())) *Batch {
	b.microtasks = queue
	return b
}

// UseSection gives the batch the host's way to run work as one of its sections: what ends a watch from a goroutine of its
// own (an aborted context's listener) runs its detach there, with the other code that touches the host's sources. It is
// called once, right after NewBatch, and returns the batch.
func (b *Batch) UseSection(run func(work func())) *Batch {
	b.section = run
	return b
}

// Section runs work as a section of the host when the batch has one (UseSection), inline otherwise.
func (b *Batch) Section(work func()) {
	if b.section == nil {
		work()
		return
	}
	b.section(work)
}

// ReportListener reports a failing watch listener's error (to the host's report function): what watches get as their
// report. Pi's `reportListener` field is a function value; the method value b.ReportListener is the same.
func (b *Batch) ReportListener(err error) {
	b.report(BatchError{Kind: BatchListenerError, Error: err})
}

// Running reports whether a commit's adoption, a delivery or a derive compute runs now: a change made now lands in this
// batch.
func (b *Batch) Running() bool { return b.running }

// AssertOutside returns invalid/reentrant inside the batch: operation may not run during adoptions or derive computes.
func (b *Batch) AssertOutside(operation string) error {
	if b.running {
		return data.Invalid(data.ReasonReentrant, operation+" inside a commit or a derive compute")
	}
	return nil
}

// AssertNotComputing returns invalid/reentrant inside a pure compute (a derive's, a registry's compose): operation would
// change a source while one is computed from it. Changes inside an adoption are fine (they land in its batch).
func (b *Batch) AssertNotComputing(operation string) error {
	if b.computing {
		return data.Invalid(data.ReasonReentrant, operation+" inside a derive compute or a compose")
	}
	return nil
}

// Compute runs the pure compute work (Run, and changes refused: AssertNotComputing); nests. It returns work's error;
// the flags are restored however work ends. Pi's `compute<T>` returns work's value: the caller captures it.
func (b *Batch) Compute(work func() error) error {
	running, computing := b.running, b.computing
	b.running, b.computing = true, true
	defer func() { b.running, b.computing = running, computing }()
	return work()
}

// Run runs work with reentrant calls refused; nests (a derive compute inside a commit).
func (b *Batch) Run(work func() error) error {
	outer := b.running
	b.running = true
	defer func() { b.running = outer }()
	return work()
}

// Commit runs one commit's adoptions, then, still synchronously, delivers a frame for every watched value they changed
// (watched derives recompute through their reads), then runs the flushes the deliveries listed. A delivery or flush that
// fails is reported; the others still run. What Within deferred while the flushes sent runs as the next commit, right
// after. An adopt that fails ends the commit: nothing is delivered and its error is returned.
func (b *Batch) Commit(adopt func() error, ctx context.Context) error {
	err := b.Run(func() error {
		defer func() {
			b.sending = false
			b.changed = nil
			b.flushes = nil
		}()
		if err := adopt(); err != nil {
			return err
		}
		// Walks the lists as they grow: a delivery may list another source.
		for i := 0; i < len(b.changed); i++ {
			if err := b.changed[i].Deliver(ctx); err != nil {
				b.report(BatchError{Kind: BatchDeliveryFailed, Error: err})
			}
		}
		b.sending = true
		for i := 0; i < len(b.flushes); i++ {
			if err := b.flushes[i](ctx); err != nil {
				b.report(BatchError{Kind: BatchDeliveryFailed, Error: err})
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	later := b.later
	if len(later) == 0 {
		return nil
	}
	b.later = nil
	return b.Commit(func() error {
		for _, each := range later {
			if err := each(); err != nil {
				return err
			}
		}
		return nil
	}, ctx)
}

// Within runs adopt inside the commit running now, which delivers what it changed; outside one, as a commit of its own;
// while a commit's flushes send, as the next commit (a link closing while it sends ends its holds: their departure
// lands in the next batch, so one commit stays one message per link and origin). For changes that happen wherever a
// reference binds or ends (host.references, host.activity). Deferred, adopt's error surfaces from the commit that runs it.
func (b *Batch) Within(adopt func() error, ctx context.Context) error {
	switch {
	case b.sending:
		b.later = append(b.later, adopt)
		return nil
	case b.running:
		return adopt()
	default:
		return b.Commit(adopt, ctx)
	}
}

// Soon runs adopt in one commit on the next microtask, together with every other Soon until then (changes arriving
// outside any commit, as a followed runtime's calls off its commit step: one batch for all of them). ctx is the first
// one's. The microtask is the queue UseMicrotasks gave; Soon panics without one, a host wiring error.
func (b *Batch) Soon(adopt func(), ctx context.Context) {
	if b.soonQueued {
		b.soon = append(b.soon, adopt)
		return
	}
	if b.microtasks == nil {
		panic("state: Batch.Soon needs a microtask queue (Batch.UseMicrotasks)")
	}
	b.soon = []func(){adopt}
	b.soonQueued = true
	b.microtasks(func() {
		soon := b.soon
		b.soon, b.soonQueued = nil, false
		// The adoptions do not fail: a Soon's adopt has no error to return.
		_ = b.Commit(func() error {
			for _, each := range soon {
				each()
			}
			return nil
		}, ctx)
	})
}

// Changed lists a watched source the current commit changed.
func (b *Batch) Changed(source Delivered) { b.changed = append(b.changed, source) }

// Flushing lists flush to run at the end of the current commit, after its deliveries (a link sending what they fed it).
func (b *Batch) Flushing(flush func(ctx context.Context) error) { b.flushes = append(b.flushes, flush) }

// Count counts one unit of derive or registry work on the host's metrics (a lazy read has no context to count
// through).
func (b *Batch) Count(counter BatchCounter) {
	if b.metrics != nil {
		b.metrics.Counter(string(counter), telemetry.InstrumentOptions{}).Add(1, nil)
	}
}

// deliveries is the scheduler the batch's watches run on, nil for the process-wide one.
func (b *Batch) deliveries() *chord.DeliveryScheduler { return b.scheduler }
