package sectioncheck

import "sync"

// Batch, Loop and chordHost stand for the host's entry points of a section.
type Batch struct{}

func (b *Batch) Section(work func()) { work() }

func (b *Batch) InSection() bool { return false }

func (b *Batch) CheckAct(check func() (act func())) (acted bool) {
	b.Section(func() {
		if act := check(); act != nil {
			act()
			acted = true
		}
	})
	return acted
}

type Loop struct{}

func (l *Loop) Run(work func()) { work() }

type read struct {
	mu         sync.Mutex
	generation int
	closed     bool
	sink       *sink
	batch      *Batch
	loop       *Loop
	next       *tiling
}

type sink struct{ runs int }

func (s *sink) adopt(run int) { s.runs += run }

type tiling struct{}

func (r *read) detached() bool { return r.closed }
func (r *read) failure() error { return nil }
func (r *read) fetch() int     { return 1 }

// preCureLoad is OpenRead.load before e8c1e17c8: overtaken is decided under the read's lock, the adoption happens in a section
// of its own.
func (r *read) preCureLoad(generation int) bool {
	r.mu.Lock()
	overtaken := r.generation != generation
	r.mu.Unlock()
	if overtaken {
		return false
	}
	run := r.fetch()
	r.batch.Section(func() { // want `acts on r.generation`
		r.sink.adopt(run)
	})
	return true
}

// curedLoad is the same through the primitive: the check is a func the section runs, so it cannot run before the section.
func (r *read) curedLoad(generation int) bool {
	run := r.fetch()
	return r.batch.CheckAct(func() func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.generation != generation {
			return nil
		}
		return func() { r.sink.adopt(run) }
	})
}

// recheckedLoad checks in the section by hand: allowed, the section reads the guarded state itself.
func (r *read) recheckedLoad(generation int) bool {
	if r.generation != generation {
		return false
	}
	adopted := false
	r.batch.Section(func() {
		if r.generation != generation {
			return
		}
		r.sink.adopt(1)
		adopted = true
	})
	return adopted
}

// failCompile: closed is read outside, the failed run is adopted in a section that does not look again.
func (r *read) failCompile() (settled bool) {
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return true
	}
	r.batch.Section(func() { // want `acts on r.closed`
		r.sink.adopt(0)
	})
	return true
}

// open is list.Open: detached is decided in one section, the tiling installed after it.
func (r *read) open() func() {
	var detached bool
	r.batch.Section(func() { detached = r.detached() })
	if detached { // want `decided this from state`
		return func() {}
	}
	r.mu.Lock()
	r.next = &tiling{}
	r.mu.Unlock()
	return func() {}
}

// openInOneSection is the cure: the decision and the install are one section.
func (r *read) openInOneSection() func() {
	r.batch.Section(func() {
		if r.detached() {
			return
		}
		r.next = &tiling{}
	})
	return func() {}
}

// verdictOnly returns what the section read and acts on nothing after it: an answer, not a decision.
func (r *read) verdictOnly() error {
	var failure error
	r.batch.Section(func() { failure = r.failure() })
	if failure != nil {
		return failure
	}
	return nil
}

// preCureOpen is reader.Open before 923604266: it blocks, then reads the reading in a section and returns what it read; the
// awaiter follows the reader in another section.
func (r *read) preCureOpen(done chan struct{}) int {
	var opened int
	<-done
	r.loop.Run(func() { // want `blocks and then reads state`
		if r.closed {
			opened = 1
		}
	})
	return opened
}

// tailOpen is the cure: the read is returned as the tail the awaiter runs in the section that settles it.
func (r *read) tailOpen(done chan struct{}) func() int {
	<-done
	return func() int {
		if r.closed {
			return 1
		}
		return 0
	}
}

// inline: a section called inside a section is the same turn, so a guard before it is not a gap.
func (r *read) inline() {
	r.batch.Section(func() {
		if r.closed {
			return
		}
		r.loop.Run(func() { r.sink.adopt(1) })
	})
}

// notState: a guard on a parameter or a local count reads no state of the object the section acts on.
func (r *read) notState(n int) {
	if n == 0 {
		return
	}
	r.batch.Section(func() { r.sink.adopt(n) })
}

// allowed: a classified site.
func (r *read) allowed() {
	if r.closed {
		return
	}
	//portlint:allow sectioncheck the act needs no fresh verdict: a closed read adopts nothing and the sink drops the run
	r.batch.Section(func() { r.sink.adopt(1) })
}

// received: what a receive yields is the result of the wait (Pi awaits there), not a read of state to guard on.
func (r *read) received(events chan int) {
	n, ok := <-events
	if !ok {
		return
	}
	r.batch.Section(func() { r.sink.adopt(n) })
}

// asked: the in-section idiom is the check and the act of one section either way.
func (r *read) asked() {
	if r.batch.InSection() {
		r.sink.adopt(1)
		return
	}
	r.batch.Section(func() { r.sink.adopt(1) })
}

// copied: a field of a struct value the function holds is its own copy, not state another section changes.
type teardown struct{ kind int }

func (r *read) copied(td teardown) {
	if td.kind == 0 {
		return
	}
	r.batch.Section(func() { r.sink.adopt(td.kind) })
}

// decideAndAct: the section checks and acts (it forks), a verdict tested after it only routes the result: the cure's own shape.
func (r *read) decideAndAct() error {
	var started *tiling
	var refused bool
	r.batch.Section(func() {
		if refused = r.detached(); refused {
			return
		}
		started = r.start(1)
	})
	if refused {
		return nil
	}
	r.next = started
	r.loop.Run(func() { r.sink.adopt(2) })
	return nil
}

func (r *read) start(n int) *tiling { return &tiling{} }
