package session

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
)

// The Session's writer (session/writer.ts): it commits the mutation line admitted, stored in batches off the line and published in
// line order. The line admits each commit as a Part once its callback and settlement succeeded; the writer sends every part
// admitted while no call is in flight as one Storage call, off the line, and publishes the parts of a call one by one, in line
// order, with a macrotask between two, so the continuations of the waits one resolves start before the next is published. Held
// commits are parts without writes, published when the parts before them are.
//
// Each part registers the keys it writes; until it is published, it is the newest unpublished writer of each, and a job that touches
// one waits for its gate. A key leaves when its newest writer is published, so the map holds one entry per key with an unpublished
// writer.
//
// Every wait of the writer (the Storage call, the hop between parts, the awaits of its own async functions) is a tick the Loop
// counts (Loop.Wait, Loop.Macrotask): a Session without a Loop waits for nothing, and stores and publishes a part as it is admitted.

// Part is one logical commit the line admitted: stored (Writes, never empty), or held in memory only (no Writes).
type Part struct {
	// Index is the line index of the job that admitted it: a failure it causes makes only later jobs' outcomes SessionFailed.
	Index int
	// Writes are the commit's writes, or nil for a commit held in memory (held.go).
	Writes []Write
	// Keys are the keys it writes: a read of one waits for its publication.
	Keys []string
	// Publish publishes it in memory, in line order, once it is durable (with its seq) or, held, once the parts before it are. A
	// non-nil result is its own failure at publication: memory could not adopt it. A part the Session made (newPart) has none: its kind
	// says how it is published.
	Publish func(seq Seq) error
	// Discard is called when it will never be published: it aborts what the part prepared.
	Discard func()

	// kind says how the Session publishes a part it made; tx is the commit it carries (partHold, partCommit), and holds and changes the
	// documents it stores the held changes of (partHolds).
	kind    partKind
	tx      *Tx
	holds   []*loadedDoc
	changes []Change

	done bool
	err  error
	// outcome, when set, resumes once, when the outcome settles: Err is nil when it was published, else the error it never was or was
	// rejected with.
	outcome Resumer
	gate    Gate
	// published is set when Publish ran; Pub is what it published, for a caller that made the commit outside a Loop.
	published bool
	pub       *Publication
}

// partKind is how the Session publishes a part it made.
type partKind uint8

const (
	// partPlain has nothing to publish in memory: the records of WriteTasks.
	partPlain partKind = iota
	// partHold is a commit held in memory (Session.publishHold).
	partHold
	// partCommit is a stored commit (Session.adoptPart).
	partCommit
	// partHolds is the store of held documents (Session.publishHolds).
	partHolds
)

// discard aborts what the part prepared, when it will never be published.
func (p *Part) discard() {
	if p.Discard != nil {
		p.Discard()
	}
	if p.tx != nil {
		p.tx.abortChanges()
	}
}

// publish publishes the part in memory.
func (p *Part) publish(s *Session, seq Seq) error {
	if p.Publish != nil {
		return p.Publish(seq)
	}
	switch p.kind {
	case partHold:
		return s.publishHold(p.tx, p)
	case partCommit:
		return s.adoptPart(p.tx, p.Writes, seq, p)
	case partHolds:
		return s.publishHolds(p, seq)
	}
	return nil
}

// Gate is the gate that opens once the part is published or never will be.
func (p *Part) Gate() *Gate { return &p.gate }

// Done reports whether the part's outcome settled: it was published, or never will be.
func (p *Part) Done() bool { return p.done }

// Err is the error the part's outcome settled with, nil when it was published and no failure rejected it after.
func (p *Part) Err() error { return p.err }

// Publication is what the part published, nil until it did, and for a part held in memory or one that stores no commit of its own.
func (p *Part) Publication() *Publication { return p.pub }

// OnOutcome resumes r once the part's outcome settles, at once when it did: Err is then its outcome.
func (p *Part) OnOutcome(r Resumer) {
	if p.done {
		r.Resume()
		return
	}
	p.outcome = r
}

func (p *Part) settle(err error) {
	if p.done {
		return
	}
	p.done, p.err = true, err
	if r := p.outcome; r != nil {
		p.outcome = nil
		r.Resume()
	}
}

// Gate opens once the part it belongs to is published or never will be; it never fails.
type Gate struct {
	opened  bool
	waiters []gateWaiter
}

// gateWaiter is what waits at a gate: fn, or, when fn is nil, r.
type gateWaiter struct {
	fn func()
	r  Resumer
}

// Opened reports whether the gate is open.
func (g *Gate) Opened() bool { return g.opened }

// Wait calls fn once the gate is open, at once when it is.
func (g *Gate) Wait(fn func()) {
	if g.opened {
		fn()
		return
	}
	g.waiters = append(g.waiters, gateWaiter{fn: fn})
}

// WaitResumer resumes r once the gate is open, at once when it is, without a closure made for it.
func (g *Gate) WaitResumer(r Resumer) {
	if g.opened {
		r.Resume()
		return
	}
	g.waiters = append(g.waiters, gateWaiter{r: r})
}

func (g *Gate) open() {
	if g.opened {
		return
	}
	g.opened = true
	waiters := g.waiters
	g.waiters = nil
	for _, w := range waiters {
		if w.fn != nil {
			w.fn()
		} else {
			w.r.Resume()
		}
	}
}

// Writer is the writer of one Session.
type Writer struct {
	s *Session
	// queue are the admitted parts not yet sent, in line order.
	queue  []*Part
	flight bool
	// unpublished is, per key, the newest unpublished part that writes it.
	unpublished map[string]*Part
	// batches and receipts are the scratch of the call in flight: one call runs at a time.
	batches  [][]Write
	receipts []receipt
	// newest is the newest admitted part that is not yet published, whose gate opens after every earlier one's.
	newest *Part
}

func (w *Writer) init(s *Session) { w.s = s; w.forgetUnpublished() }

// forgetUnpublished starts the map of unpublished keys empty.
func (w *Writer) forgetUnpublished() { w.unpublished = map[string]*Part{} }

// wait runs r after n ticks; without a Loop, or for none, at once.
func (w *Writer) wait(n int, r Resumer) {
	if n <= 0 || w.s.loop == nil {
		r.Resume()
		return
	}
	w.s.loop.Wait(n, r)
}

// Admit registers a part's keys, queues it, and sends it at once unless a call is in flight.
func (w *Writer) Admit(p *Part) {
	for _, key := range p.Keys {
		w.unpublished[key] = p
	}
	w.newest = p
	w.queue = append(w.queue, p)
	w.pump()
}

// Gate is the gate of the newest unpublished writer of key, or nil when it has none.
func (w *Writer) Gate(key string) *Gate {
	if p := w.unpublished[key]; p != nil {
		return &p.gate
	}
	return nil
}

// Drained is the gate that opens once every part admitted so far is published or never will be, or nil when there is none.
func (w *Writer) Drained() *Gate {
	if w.newest == nil {
		return nil
	}
	return &w.newest.gate
}

// Unpublished is the number of keys with an unpublished writer.
func (w *Writer) Unpublished() int { return len(w.unpublished) }

// Busy reports whether a part is queued or a call is in flight.
func (w *Writer) Busy() bool { return w.flight || len(w.queue) != 0 }

func (w *Writer) pump() {
	if w.flight || len(w.queue) == 0 {
		return
	}
	w.flight = true
	parts := w.queue
	w.queue = nil
	c := &call{w: w, parts: parts}
	c.send()
}

// sessionFailed is SessionFailed with the Session's failure.
func (w *Writer) sessionFailed(err error) error { return &FailedError{Cause: err} }

// failed is SessionFailed with the Session's failure, or with err until the Session records it.
func (w *Writer) failed(err error) error {
	if w.s.failure != nil {
		return w.sessionFailed(w.s.failure)
	}
	return w.sessionFailed(err)
}

// unpublish settles a part that will never be published: it discards it, rejects it with err and opens its gate.
func (w *Writer) unpublish(p *Part, err error) {
	p.discard()
	p.settle(err)
	w.unregister(p)
	w.open(p)
}

// unregister drops the keys a part is still the newest writer of: it is published, or never will be.
func (w *Writer) unregister(p *Part) {
	for _, key := range p.Keys {
		if w.unpublished[key] == p {
			delete(w.unpublished, key)
		}
	}
	if len(w.unpublished) == 0 && len(p.Keys) > unpublishedKeep {
		// A map keeps the buckets of its largest size: the one a part of thousands of keys grew is let go.
		w.forgetUnpublished()
	}
}

// unpublishedKeep is the number of keys of one part past which the map of unpublished keys is made again once it empties.
const unpublishedKeep = 1024

// open opens a part's gate: the jobs waiting for it go on. The newest part's opens Drained too.
func (w *Writer) open(p *Part) {
	p.gate.open()
	if w.newest == p {
		w.newest = nil
	}
}

type callStep uint8

const (
	stepRead callStep = iota + 1
	stepStore
	stepResume
	stepHop
	stepHopped
	stepFailed
	stepEnd
	stepPublished
)

// failEntry is the outcome of a part of a call that did not publish and resolve: whether it was published, and what its outcome is:
// an error of its own (any value, nil included for none), or SessionFailed.
type failEntry struct {
	part      *Part
	own       bool
	err       error
	published bool
}

// call is one Storage call of the writer and the publication of its parts (writer.ts #flush, #send, #publish).
type call struct {
	w      *Writer
	parts  []*Part
	stored []*Part
	step   callStep
	// resume is the ticks from the Storage's receipts to the writer's resumption from the call.
	resume int
	// toReceive is the ticks from the Storage's read of its ID counter to its receipts.
	toReceive int
	// nextID is the Session's next unminted ID when the Storage read its candidateNextId: as the call started, or after its last batch was
	// applied when the host times the call (Loop.StoreTicks).
	nextID ID

	// receipts are the Storage's receipts, by stored index.
	receipts []receipt
	result   CommitResult
	// rejected is the error of a call that rejected.
	rejected    error
	hasRejected bool

	// The publication loop: the next part, how many were published (a macrotask separates two), and the stored index of the part
	// being published (-1 for a held one).
	next     int
	count    int
	storedAt int
	index    int
	hopped   bool
	// The part the host's Storage scope publishes (Session.Publishing): whether it ran, whether it ran inside the scope's own call, and the
	// part and seq it publishes. onceFn is once as the func the host calls.
	onceRan, onceInCall bool
	oncePart            *Part
	onceSeq             Seq
	onceFn              func()

	// parked is set while the host adopts the held part heldPart (Session.HeldHost): the publication goes on from its answer.
	parked   bool
	heldPart *Part
	heldSeq  Seq

	stopped bool
	stop    stopInfo
	failing []failEntry
}

// receipt is the seq the Storage gave one stored part, once it did.
type receipt struct {
	seq  Seq
	have bool
}

type stopInfo struct {
	err      error
	at       int
	external bool
}

func (c *call) Resume() {
	switch c.step {
	case stepRead:
		// The Storage reads its ID counter once the last batch is applied (writer.ts: SqliteStorage.commit's candidateNextId).
		c.nextID = c.w.s.storageNextID()
		c.step = stepStore
		c.w.wait(c.toReceive, c)
	case stepStore:
		c.store()
	case stepResume:
		c.afterCommit()
	case stepHop:
		// The macrotask resolved the hop: `#publish` resumes from `await hop()` one tick later.
		c.step = stepHopped
		c.w.wait(hopcost.PublishResume, c)
	case stepHopped:
		c.hopped = true
		c.publish()
	case stepFailed:
		c.fail()
	case stepEnd:
		clear(c.w.batches)
		c.w.flight = false
		c.w.pump()
	case stepPublished:
		c.publish()
	}
}

// send stores the parts and publishes them: writer.ts #send up to its first await.
func (c *call) send() {
	w := c.w
	for _, p := range c.parts {
		if p.Writes != nil {
			c.stored = append(c.stored, p)
		}
	}
	if failure := w.s.failure; failure != nil {
		for _, p := range c.parts {
			w.unpublish(p, w.sessionFailed(failure))
		}
		c.end(hopcost.WriterRepumpUnsent)
		return
	}
	if len(c.stored) == 0 {
		c.afterCommit()
		return
	}
	batches := w.batches[:0]
	c.receipts = w.receipts[:0]
	for _, p := range c.stored {
		batches = append(batches, p.Writes)
		c.receipts = append(c.receipts, receipt{})
	}
	w.batches, w.receipts = batches, c.receipts
	c.nextID = w.s.nextID
	if w.s.CommitBatches != nil {
		// The host's Storage takes the call: it answers through receive and done, in the tick the writer resumes.
		w.s.CommitBatches(batches, c.nextID, c.receive, c.done)
		return
	}
	read, receive, resume := 0, 0, 0
	if w.s.loop != nil && w.s.loop.StoreTicks != nil {
		read, receive, resume = w.s.loop.StoreTicks(batches)
	}
	receive = max(receive, 0)
	read = min(max(read, 0), receive)
	c.resume = max(resume-receive, 0)
	c.toReceive = receive - read
	c.step = stepRead
	w.wait(read, c)
}

// receive is the durability receipt of batch index.
func (c *call) receive(index int, seq Seq) { c.receipts[index] = receipt{seq: seq, have: true} }

// done is the Storage call's answer: the writer resumes from `await this.#host.commit(...)`.
func (c *call) done(result CommitResult, err error) {
	c.result, c.rejected, c.hasRejected = result, err, err != nil
	c.afterCommit()
}

// store is Storage.commit over the Session's Store: every batch durable, in order, each checked against the state the earlier ones left,
// and the receipts.
func (c *call) store() {
	w := c.w
	c.result, c.rejected = w.s.commitBatches(c.stored, c.nextID, c.receive)
	c.hasRejected = c.rejected != nil
	c.step = stepResume
	w.wait(c.resume, c)
}

func (c *call) afterCommit() {
	w := c.w
	// A call that settles once another failure failed the Session reports SessionFailed with that first error, whatever it
	// returned (spec section 10.1), and publishes nothing.
	if failure := w.s.failure; failure != nil {
		for _, p := range c.parts {
			w.unpublish(p, w.sessionFailed(failure))
		}
		c.end(hopcost.WriterRepumpUnsent)
		return
	}
	c.publish()
}

// parkHop waits for a macrotask: what the parts published so far resolved runs before the next is published.
func (c *call) parkHop() bool {
	l := c.w.s.loop
	if l == nil {
		return false
	}
	c.step = stepHop
	l.Macrotask(c)
	return true
}

// publish publishes the received parts and held ones in order, a macrotask apart, and settles every part (group commit section 3.4). A
// refusal, a rejection, or a part whose adoption throws fails the Session once the parts before it are published and the host
// observers got the last one; the parts after it are not published, and reject with SessionFailed. What the failure decides (the
// outcomes, and the gates of the parts it concerns) is settled only once the Session failed, so no job waiting at such a gate, and no
// later outcome, goes on as if it had not.
func (c *call) publish() {
	w := c.w
	for c.next < len(c.parts) {
		part := c.parts[c.next]
		if !c.hopped {
			c.index = -1
			if part.Writes != nil {
				c.index = c.storedAt
				c.storedAt++
			}
			if c.stopped {
				// A part after a rejected call's last receipt has the call's error; any other part after a stop was not sent,
				// nor any part once something else failed the Session.
				entry := failEntry{part: part}
				if c.hasRejected && c.index >= 0 && !c.stop.external {
					entry.own, entry.err = true, c.rejected
				}
				c.failing = append(c.failing, entry)
				c.next++
				continue
			}
			if c.index >= 0 && !c.receipts[c.index].have {
				err := c.rejected
				if !c.hasRejected {
					if c.index == c.result.Stored && c.result.Refused != nil {
						err = c.result.Refused
					} else {
						err = errors.New("Storage committed without adopting the batch")
					}
				}
				c.failing = append(c.failing, failEntry{part: part, own: true, err: err})
				c.stopped, c.stop = true, stopInfo{err: err, at: part.Index}
				c.next++
				continue
			}
			// A macrotask between two parts: what the first resolves runs before the second is published.
			first := c.count == 0
			c.count++
			if !first && c.parkHop() {
				return
			}
		}
		c.hopped = false
		var seq Seq
		if c.index >= 0 {
			seq = c.receipts[c.index].seq
		}
		// The publication runs inside the Storage's own scope when the host has one (Session.Publishing): it runs once, when the host says, and
		// the writer goes on one tick after it (`await publication`).
		if pub := w.s.Publishing; pub != nil {
			c.onceRan, c.onceInCall, c.oncePart, c.onceSeq = false, true, part, seq
			if c.onceFn == nil {
				c.onceFn = c.once
			}
			handled := pub(c.onceFn)
			c.onceInCall = false
			if handled && !c.onceRan {
				return
			}
			if !c.onceRan {
				c.once()
			}
			continue
		}
		c.publishPart(part, seq)
		if c.parked {
			return
		}
	}
	if c.hasRejected || c.stopped {
		// After the host dispatch of the last part published, which is one microtask away.
		c.step = stepFailed
		w.wait(hopcost.FailureSettle, c)
		return
	}
	c.settle()
}

// once is the publication of the part the host's Storage scope runs (Session.Publishing), once.
func (c *call) once() {
	if c.onceRan {
		return
	}
	c.onceRan = true
	c.publishPart(c.oncePart, c.onceSeq)
	if !c.onceInCall {
		c.step = stepPublished
		c.w.wait(1, c)
	}
}

// publishPart publishes one part of the call, in the scope the host gave it, and advances the call to the next part, unless the host
// adopts it first (c.parked).
func (c *call) publishPart(part *Part, seq Seq) {
	if c.publishOne(part, seq) {
		return
	}
	c.next++
}

// publishOne publishes one part: nothing more of the call is published once something else failed the Session, and a part whose
// adoption failed, or whose call rejected, settles when the call's failure does. It reports whether it parked the call for the host's
// adoption of a held part, which finishOne completes.
func (c *call) publishOne(part *Part, seq Seq) bool {
	w := c.w
	// Failed by something else meanwhile, during the hop: nothing more of the call is published. Checked right before it would be, after every wait.
	if external := w.s.failure; external != nil {
		c.stopped, c.stop = true, stopInfo{err: external, at: part.Index, external: true}
		c.failing = append(c.failing, failEntry{part: part})
		return false
	}
	if w.s.HeldHost != nil && w.s.Publishing == nil && part.Publish == nil && part.kind == partHold {
		c.parked, c.heldPart, c.heldSeq = true, part, seq
		w.s.HeldHost.AdoptHeld(c)
		return true
	}
	c.finishOne(part, seq, nil)
	return false
}

// HeldAdopter is told the outcome of the host's adoption of a held commit: nil when it adopted it.
type HeldAdopter interface{ Adopted(err error) }

// Adopted is the host's adoption of the held part the call parked on: the publication goes on.
func (c *call) Adopted(err error) {
	part := c.heldPart
	c.parked, c.heldPart = false, nil
	c.finishOne(part, c.heldSeq, err)
	c.next++
	c.publish()
}

// finishOne is the rest of publishOne: the part's publication in memory, after the host's adoption of it (hostErr) when it is a held one.
func (c *call) finishOne(part *Part, seq Seq, hostErr error) {
	w := c.w
	// Its keys leave first, so what its publication schedules sees it published; its waiters wake after it. A held part keeps them
	// until the host adopted it.
	w.unregister(part)
	adoption := hostErr
	switch {
	case adoption != nil:
		// Memory could not adopt it: nothing of it was published.
	case c.index >= 0 && c.result.Adoption != nil && c.index == c.result.AdoptionAt:
		adoption = c.result.Adoption
	default:
		adoption = part.publish(w.s, seq)
	}
	part.published = true
	if adoption != nil {
		c.failing = append(c.failing, failEntry{part: part, own: true, err: adoption, published: true})
		c.stopped, c.stop = true, stopInfo{err: adoption, at: part.Index}
		return
	}
	// A part published while its call rejected is durable but rejects (spec section 10.1).
	if c.hasRejected {
		c.failing = append(c.failing, failEntry{part: part, own: true, err: c.rejected, published: true})
		return
	}
	if failure := w.s.failure; failure != nil {
		part.settle(w.sessionFailed(failure))
	} else {
		part.settle(nil)
	}
	w.open(part)
}

// fail fails the Session for the call, then settles what it decided.
func (c *call) fail() {
	w := c.w
	// Every part of the call that rejected keeps the storage error; the first of them is the failure's boundary.
	var own []int
	for _, e := range c.failing {
		if e.own {
			own = append(own, e.part.Index)
		}
	}
	if c.hasRejected {
		// Without a part of its own (something else failed the Session before the first publication) the boundary is the Session's default,
		// as Pi's `own[0]!` is undefined there.
		at := w.s.failBoundary()
		if len(own) > 0 {
			at = own[0]
		}
		w.s.failAt(c.rejected, at, own)
	} else {
		w.s.failAt(c.stop.err, c.stop.at, own)
	}
	c.settle()
}

// settle settles the parts that did not publish and resolve, and rejects what was admitted while the call ran after a failure.
func (c *call) settle() {
	w := c.w
	for _, e := range c.failing {
		err := e.err
		if !e.own {
			err = w.failed(c.stop.err)
		}
		if e.published {
			e.part.settle(err)
			w.open(e.part)
		} else {
			w.unpublish(e.part, err)
		}
	}
	// Admitted while this call ran, after the failure: never sent.
	if failure := w.s.failure; failure != nil {
		queued := w.queue
		w.queue = nil
		for _, p := range queued {
			w.unpublish(p, w.sessionFailed(failure))
		}
	}
	c.end(hopcost.WriterRepump)
}

// end clears the flight and pumps after n ticks: `#flush` resumes from `await this.#send(...)`.
func (c *call) end(n int) {
	c.step = stepEnd
	c.w.wait(n, c)
}
