// The Session's writer: commits the mutation line admitted, stored in batches off the line and published in line order.
//
// Ports packages/durable/src/session/writer.ts
//
// Go mapping decisions:
//   - Upstream's async `#flush` is one goroutine the writer owns: it runs while parts are queued or in flight, ends when none
//     are, and Wait joins it (the Session's close waits for it after the drain). `#flight` is set while it runs.
//   - A Part's `outcome` promise is Wait (blocking, returns the rejection as the error) and OnSettled (the callback runs on the
//     writer's goroutine at the moment the outcome settles: the Session's outcome fences are ordered by it, not by a caller's
//     wake-up). A part's `gate` promise is Gate, a channel closed once it is published or never will be.
//   - Upstream's macrotask hop between two parts is WriterHost.Hop: the Session returns from it once the goroutines the earlier
//     publication woke (the callers of the commits it resolved, the registered waits its observers woke) have resumed, which is
//     the Go form of "the continuations of what one part resolves start before the next is published" (see the Session header
//     and resumption.go).
//   - Upstream's `Storage.publishing` scope and the microtask dispatch after it are WriterHost.Publishing: it returns once the
//     publication ran inside the storage's scope and the host dispatch and the wakes it queued ran after it. A nil channel means
//     it ran at once.
//   - A part's keys leave the unpublished set after its own observers ran (upstream unregisters first). Upstream's publication is
//     atomic with respect to the code that reads keys; here the observers run on this goroutine while another goroutine may touch
//     a key, so a key must stay until the state the observers keep in step with is.
package session

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/durable"
)

// PublishFailureKind says what failed a part at publication.
type PublishFailureKind int

const (
	// PublishAdoption is memory that could not adopt the part.
	PublishAdoption PublishFailureKind = iota + 1
	// PublishListener is one of the Session's own observers that panicked.
	PublishListener
)

// PublishFailure is a part's own failure at publication: memory could not adopt it, or one of the Session's own observers
// panicked.
type PublishFailure struct {
	Kind PublishFailureKind
	Err  error
}

// PartSpec is one logical commit the line admitted: stored (Writes, never empty), or held in memory only (no Writes).
type PartSpec struct {
	// Index is the line index of the job that admitted it: a failure it causes makes only later jobs' outcomes SessionFailed.
	Index  int
	Writes []durable.StorageWrite
	// Keys are the keys it writes: a read of one waits for its publication.
	Keys []string
	Ctx  context.Context
	// Publish publishes it in memory, in line order, once it is durable (with its seq) or, held, once the parts before it are
	// (seq is then 0).
	Publish func(seq durable.Seq) *PublishFailure
	// Published, when set, runs right after its keys left the unpublished set, still inside its publication: what only
	// schedules work (an eviction sweep a document's use arms), which has to see the keys gone.
	Published func()
	// Discard is called when it will never be published: it aborts what it prepared.
	Discard func()
}

// Part is an admitted part and its outcome.
type Part struct {
	PartSpec

	mu      sync.Mutex
	settled bool
	err     error
	waiters []func(error)
	// outcome is closed once it is settled.
	outcome chan struct{}
	// gate is closed once it is published or never will be; it never carries an error.
	gate     chan struct{}
	gateOnce sync.Once
	// touches counts the calls of Gate that returned the part's gate while it was unpublished, guarded by Writer.mu.
	touches int
}

// Wait blocks until the part is published, which returns nil, or will never be: the error it rejects with. A part rejects
// also after publication when its storage call rejected or the Session failed meanwhile.
func (part *Part) Wait() error {
	await(part.outcome)
	part.mu.Lock()
	defer part.mu.Unlock()
	return part.err
}

// OnSettled runs fn with the part's outcome once it is settled, at once when it is. It runs on the goroutine that settles the
// part, so it must be quick and never block.
func (part *Part) OnSettled(fn func(err error)) {
	part.mu.Lock()
	if !part.settled {
		part.waiters = append(part.waiters, fn)
		part.mu.Unlock()
		return
	}
	err := part.err
	part.mu.Unlock()
	fn(err)
}

func (part *Part) isSettled() bool {
	part.mu.Lock()
	defer part.mu.Unlock()
	return part.settled
}

// settle fixes the part's outcome: the first call wins, as a promise settles once.
func (part *Part) settle(err error) {
	part.mu.Lock()
	if part.settled {
		part.mu.Unlock()
		return
	}
	part.settled = true
	part.err = err
	waiters := part.waiters
	part.waiters = nil
	close(part.outcome)
	part.mu.Unlock()
	for _, waiter := range waiters {
		waiter(err)
	}
}

// failedOutcome is how a part that will not resolve settles: with an error of its own, or SessionFailed.
type failedOutcome struct {
	own bool
	err error
}

var sessionFailedOutcome = failedOutcome{}

// WriterHost is what the writer needs of its Session.
type WriterHost interface {
	Commit(ctx context.Context, batches [][]durable.StorageWrite, receive func(index int, seq durable.Seq)) (durable.CommitResult, error)
	// Stored applies a stored batch to the Session's read caches, right before it is published.
	Stored(writes []durable.StorageWrite)
	// Publishing runs one part's publication inside the Storage's own scope (Storage.Publishing); it returns nil when the
	// publication ran at once, else a channel closed once it ran. Whatever the publication queued for after its scope (the
	// dispatch to the host's observers, the waits it woke) has run when the channel is closed or it returned nil.
	Publishing(publish func(), ctx context.Context) <-chan struct{}
	// Hop returns once what the previous part's publication woke has resumed: the macrotask between two publications.
	Hop()
	// Failure is the Session's failure, nil when it has not failed.
	Failure() error
	// Fail fails the Session; at is the line index of the first job whose outcome the failure is, and own the jobs after it
	// whose outcome is still their own error (the other parts of a call that rejected), not SessionFailed.
	Fail(err error, at int, own []int)
	// Report reports an own observer's panic that cannot fail the Session, as it fails with a storage error.
	Report(err error, ctx context.Context)
}

// Writer is the writer of one Session. The line admits each commit as a part once its callback and settlement succeeded; the
// writer sends every part admitted while no call is in flight as one Commit call, off the line, and publishes the parts of a
// call one by one, in line order, with a hop between two, so the continuations of the waits one resolves start before the next
// is published. Held commits are parts without writes, published when the parts before them are.
//
// Each part registers the keys it writes; until it is published, it is the newest unpublished writer of each, and a job that
// touches one waits for its gate. A key leaves when its newest writer is published, so the map holds one entry per key with an
// unpublished writer.
type Writer struct {
	host WriterHost

	// mu guards queue, flight, unpublished, and newest. It is never held across a call into the host, so a reader of a gate
	// never waits for a publication.
	mu sync.Mutex
	// queue holds the admitted parts not yet sent, in line order.
	queue  []*Part
	flight bool
	// unpublished is, per key, the newest unpublished part that writes it.
	unpublished map[string]*Part
	// newest is the newest admitted part that is not yet published, whose gate opens after every earlier one's.
	newest  *Part
	running sync.WaitGroup
}

// NewWriter returns the writer of host.
func NewWriter(host WriterHost) *Writer {
	return &Writer{host: host, unpublished: map[string]*Part{}}
}

// Admit admits a part: it registers its keys, queues it, and sends it at once unless a call is in flight.
func (writer *Writer) Admit(spec PartSpec) *Part {
	part := &Part{PartSpec: spec, outcome: make(chan struct{}), gate: make(chan struct{})}
	writer.mu.Lock()
	for _, key := range spec.Keys {
		writer.unpublished[key] = part
	}
	writer.newest = part
	writer.queue = append(writer.queue, part)
	writer.mu.Unlock()
	writer.pump()
	return part
}

// Gate returns the gate of the newest unpublished writer of key, or nil when it has none.
func (writer *Writer) Gate(key string) <-chan struct{} {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if part := writer.unpublished[key]; part != nil {
		part.touches++
		return part.gate
	}
	return nil
}

// Touches is the number of Gate calls that returned the gate of the newest unpublished writer of key, whoever made them (a
// job's Touch, a retention probe): for tests that wait until the one caller they expect has reached the wait for that
// publication; 0 when key has no unpublished writer.
func (writer *Writer) Touches(key string) int {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if part := writer.unpublished[key]; part != nil {
		return part.touches
	}
	return 0
}

// Drained returns a channel closed once every part admitted so far is published or never will be; nil when there is none.
func (writer *Writer) Drained() <-chan struct{} {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.newest != nil {
		return writer.newest.gate
	}
	return nil
}

// Unpublished is the number of keys with an unpublished writer, for tests that bound memory.
func (writer *Writer) Unpublished() int {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return len(writer.unpublished)
}

// Queued is the number of admitted parts not yet sent, for tests that wait for what is admitted behind a call in flight.
func (writer *Writer) Queued() int {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return len(writer.queue)
}

// Wait returns once the writer's goroutine ended: nothing is queued or in flight.
func (writer *Writer) Wait() { writer.running.Wait() }

func (writer *Writer) pump() {
	writer.mu.Lock()
	if writer.flight || len(writer.queue) == 0 {
		writer.mu.Unlock()
		return
	}
	writer.flight = true
	parts := writer.queue
	writer.queue = nil
	writer.running.Add(1)
	writer.mu.Unlock()
	go func() {
		defer writer.running.Done()
		writer.flush(parts)
	}()
}

// flush sends and publishes parts, then what was admitted meanwhile. A panic the writer did not expect fails the Session and
// settles the parts (abandon), so no outcome or gate is stranded.
func (writer *Writer) flush(parts []*Part) {
	for len(parts) > 0 {
		writer.sendGuarded(parts)
		writer.mu.Lock()
		parts = writer.queue
		writer.queue = nil
		if len(parts) == 0 {
			writer.flight = false
		}
		writer.mu.Unlock()
	}
}

func (writer *Writer) sendGuarded(parts []*Part) {
	defer func() {
		if recovered := recover(); recovered != nil {
			writer.abandon(parts, panicError(recovered))
		}
	}()
	writer.send(parts)
}

// send stores parts in one call, then publishes them in order; it settles every one of them.
func (writer *Writer) send(parts []*Part) {
	var stored []*Part
	for _, part := range parts {
		if part.Writes != nil {
			stored = append(stored, part)
		}
	}
	seqs := make([]durable.Seq, len(stored))
	received := make([]bool, len(stored))
	var result *durable.CommitResult
	var rejected error
	if failed := writer.host.Failure(); failed != nil {
		for _, part := range parts {
			writer.unpublish(part, durable.NewSessionFailed(failed))
		}
		return
	}
	if len(stored) > 0 {
		batches := make([][]durable.StorageWrite, len(stored))
		for index, part := range stored {
			batches[index] = part.Writes
		}
		committed, err := writer.host.Commit(context.WithoutCancel(stored[0].Ctx), batches, func(index int, seq durable.Seq) {
			seqs[index] = seq
			received[index] = true
		})
		if err != nil {
			rejected = err
		} else {
			result = &committed
		}
	}
	// A call that settles once another failure failed the Session reports SessionFailed with that first error, whatever it
	// returned (spec section 10.1), and publishes nothing.
	if failed := writer.host.Failure(); failed != nil {
		for _, part := range parts {
			writer.unpublish(part, durable.NewSessionFailed(failed))
		}
		return
	}
	writer.publish(parts, seqs, received, result, rejected)
}

// publishStop is why a call stopped publishing.
type publishStop struct {
	err error
	at  int
	// external is set when something else failed the Session.
	external bool
}

// failingPart is a part settled once the Session failed: whether it was published, and its outcome.
type failingPart struct {
	part      *Part
	outcome   failedOutcome
	published bool
}

// publish publishes the received parts and held ones in order, a hop apart, and settles every part (group commit section 3.4). A
// refusal, a rejection, or a part whose adoption or own observer panics fails the Session once the parts before it are
// published and the host observers got the last one; the parts after it are not published, and reject with SessionFailed. What
// the failure decides (the outcomes, and the gates of the parts it concerns) is settled only once the Session failed, so no job
// waiting at such a gate, and no later outcome, goes on as if it had not.
func (writer *Writer) publish(parts []*Part, seqs []durable.Seq, received []bool, result *durable.CommitResult, rejected error) {
	var stop *publishStop
	var failing []failingPart
	storedIndex, published := 0, 0
	for _, part := range parts {
		index := -1
		if part.Writes != nil {
			index = storedIndex
			storedIndex++
		}
		if stop != nil {
			// A part after a rejected call's last receipt has the call's error; any other part after a stop was not sent, nor
			// any part once something else failed the Session.
			outcome := sessionFailedOutcome
			if rejected != nil && index >= 0 && !stop.external {
				outcome = failedOutcome{own: true, err: rejected}
			}
			failing = append(failing, failingPart{part: part, outcome: outcome})
			continue
		}
		var seq durable.Seq
		if index >= 0 && !received[index] {
			var err error
			switch {
			case rejected != nil:
				err = rejected
			case result != nil && index == result.Stored && result.Refused != nil:
				err = result.Refused
			default:
				err = errors.New("Storage committed without adopting the batch")
			}
			failing = append(failing, failingPart{part: part, outcome: failedOutcome{own: true, err: err}})
			stop = &publishStop{err: err, at: part.Index}
			continue
		}
		if index >= 0 {
			seq = seqs[index]
		}
		// A hop between two parts: what the first resolves runs before the second is published. pig divergence (D224): the hop waits
		// for the listeners of the part before it, which a listener that blocks on a wait the host cannot see holds up (WriterHost.Hop).
		if published > 0 {
			writer.host.Hop()
		}
		published++
		var failure *PublishFailure
		// Failed by something else meanwhile, during the hop or before a storage's scope runs the publication: nothing more of
		// the call is published. Checked right before it would be, after every wait.
		var external error
		publication := writer.host.Publishing(func() {
			external = writer.host.Failure()
			if external != nil {
				return
			}
			failure = writer.publishPart(part, seq)
		}, part.Ctx)
		if publication != nil {
			<-publication
		}
		if external != nil {
			stop = &publishStop{err: external, at: part.Index, external: true}
			failing = append(failing, failingPart{part: part, outcome: sessionFailedOutcome})
			continue
		}
		if failure != nil && failure.Kind == PublishAdoption {
			failing = append(failing, failingPart{part: part, outcome: failedOutcome{own: true, err: failure.Err}, published: true})
			stop = &publishStop{err: failure.Err, at: part.Index}
			continue
		}
		// A part published while its call rejected is durable but rejects (spec section 10.1); an own observer's panic is then
		// reported, as the Session fails with the storage error.
		if rejected != nil {
			if failure != nil {
				writer.host.Report(failure.Err, part.Ctx)
			}
			failing = append(failing, failingPart{part: part, outcome: failedOutcome{own: true, err: rejected}, published: true})
			continue
		}
		// An own observer's panic: the commit stands; the Session fails after it. Upstream's failure is one microtask after
		// the last publication, before the continuation of the commit that caused it, so the Session has failed, and its close
		// listeners ran, when that commit's caller resumes: it fails here, before the part resolves.
		if failure != nil {
			stop = &publishStop{err: failure.Err, at: part.Index}
			writer.host.Fail(failure.Err, part.Index, nil)
		}
		if failedMeanwhile := writer.host.Failure(); failedMeanwhile != nil && failure == nil {
			part.settle(durable.NewSessionFailed(failedMeanwhile))
		} else {
			part.settle(nil)
		}
		writer.open(part)
	}
	if rejected != nil || stop != nil {
		// Every part of the call that rejected keeps the storage error; the first of them is the failure's boundary.
		var own []int
		for _, entry := range failing {
			if entry.outcome.own {
				own = append(own, entry.part.Index)
			}
		}
		cause, at := rejected, parts[0].Index
		if len(own) > 0 {
			at = own[0]
		}
		if rejected == nil {
			cause, at = stop.err, stop.at
		}
		writer.host.Fail(cause, at, own)
	}
	for _, entry := range failing {
		err := entry.outcome.err
		if !entry.outcome.own {
			cause := error(nil)
			if stop != nil {
				cause = stop.err
			}
			err = writer.failed(cause)
		}
		if entry.published {
			entry.part.settle(err)
			writer.open(entry.part)
		} else {
			writer.unpublish(entry.part, err)
		}
	}
	// Admitted while this call ran, after the failure: never sent.
	if failure := writer.host.Failure(); failure != nil {
		writer.mu.Lock()
		queued := writer.queue
		writer.queue = nil
		writer.mu.Unlock()
		for _, part := range queued {
			writer.unpublish(part, durable.NewSessionFailed(failure))
		}
	}
}

// publishPart publishes one part: the read caches take its writes, then it publishes itself; its keys leave once its own
// observers ran, then Published runs (what only schedules work, such as an eviction sweep, which must see the keys gone). A
// panic anywhere in it, the read caches included, leaves memory behind what is stored, like an adoption's.
func (writer *Writer) publishPart(part *Part, seq durable.Seq) (failure *PublishFailure) {
	left := false
	leave := func() {
		if !left {
			left = true
			writer.unregister(part)
		}
	}
	defer leave()
	defer func() {
		if recovered := recover(); recovered != nil {
			failure = &PublishFailure{Kind: PublishAdoption, Err: panicError(recovered)}
		}
	}()
	if part.Writes != nil {
		writer.host.Stored(part.Writes)
	}
	failure = part.Publish(seq)
	leave()
	if part.Published != nil {
		part.Published()
	}
	return failure
}

// abandon is for a panic the writer itself did not expect while sending or publishing parts (a bug): it fails the Session at the
// first of them and settles every one not settled yet, so no outcome, gate, or close waits on it forever.
func (writer *Writer) abandon(parts []*Part, err error) {
	writer.host.Fail(err, parts[0].Index, nil)
	for _, part := range parts {
		if !part.isSettled() {
			writer.unpublish(part, writer.failed(err))
		}
		writer.unregister(part)
		writer.open(part)
	}
}

// failed is SessionFailed with the Session's failure, or with err until the Session records it.
func (writer *Writer) failed(err error) error {
	if failure := writer.host.Failure(); failure != nil {
		return durable.NewSessionFailed(failure)
	}
	return durable.NewSessionFailed(err)
}

// unpublish settles a part that will never be published: it discards it, rejects it with err, and opens its gate.
func (writer *Writer) unpublish(part *Part, err error) {
	part.Discard()
	part.settle(err)
	writer.unregister(part)
	writer.open(part)
}

// unregister drops the keys a part is still the newest writer of: it is published, or never will be.
func (writer *Writer) unregister(part *Part) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	for _, key := range part.Keys {
		if writer.unpublished[key] == part {
			delete(writer.unpublished, key)
		}
	}
}

// open opens a part's gate: the jobs waiting for it go on. The newest part's opens Drained too.
func (writer *Writer) open(part *Part) {
	part.gateOnce.Do(func() { close(part.gate) })
	writer.mu.Lock()
	if writer.newest == part {
		writer.newest = nil
	}
	writer.mu.Unlock()
}

// panicError is the error a recovered panic value stands for.
func panicError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return fmt.Errorf("%v", recovered)
}
