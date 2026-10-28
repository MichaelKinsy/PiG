package session

import "errors"

// Loop is the core's stand-in for the JavaScript event loop and the Session
// line. Continuations that Pi runs as promise reactions or queueMicrotask
// callbacks run here as queue items in FIFO order; a commit job runs to
// completion in one item, so jobs serialize as they do on Pi's mutation line.
//
// A job whose store read needs the host (*NeedRead) parks the Loop with that
// request: Run returns it, and after the host answered, Run resumes with the
// same job from the start. Nothing else runs while a job is parked, as nothing
// else runs while Pi's line awaits storage.
type Loop struct {
	// Blocked reports that something outside the Loop holds the Session line (a host transaction, ABI section 8): queued line
	// jobs wait until it reports false and Kick runs them.
	Blocked func() bool
	// Admit, when set, is the admission check of a line job as it is enqueued (session.ts #assertUsable): a job it
	// rejects never runs, and its done receives the error as a microtask.
	Admit func() error
	// Session, when set, is the Session whose commits the line jobs make: a job's publication runs at the job's
	// completion, with its done, rather than inside the job.
	Session *Session
	// Hold, when set, is asked as each line job finishes whether its completion (publication, done, and the release of
	// the line) waits for Pi's duration of the job (dcore-trace A1; ABI section 5.3): it returns true after it parked
	// release, and calls release once that time passed. Line jobs queued meanwhile wait behind it.
	Hold func(release func()) bool
	// Tail, when set, is asked as a job finishes whether the line stays held for one more tick: it returns true after it parked
	// release. session.ts #enqueue :530-537 starts a job through `this.#tail.then(job)` where the tail is `run.then(...)` of the
	// previous job, so the next job starts a tick after the finished one, a read job or a commit job: a job queued behind it, and
	// one its publication or continuation queues as it finishes, as Pi queues those before the run resolves. A bare job (a host
	// transaction's turn) holds the line only for a job queued behind it.
	Tail func(release func()) bool
	// Begin, when set, is asked before a job queued on an idle line through LineAfterTail or ReadLine starts whether the line
	// waits one tick for it: it returns true after it parked start. session.ts #enqueue :530-531 starts a job through
	// `this.#tail.then(job)`, a tick after the call when the tail is already resolved.
	Begin func(start func()) bool
	// Hops, when set, parks r on a chain of n hops and returns true; r.Resume runs once the chain ran, at once in the host's hopped step.
	// The host keeps r itself, not a closure over it, so a continuation that carries its own state (a job, an invocation) costs the
	// host no allocation.
	Hops func(n int, r Resumer) bool
	// Answered is set while a Tail is asked after a ClientReadLine job answered its caller a hop before its end (Settle): the tail
	// then holds the line for the hop it gave up, so the next job starts where Pi's does.
	Answered bool
	// StartDelay is the ticks an idle-line job waited for its start (Begin) that its Hold adds to the job's duration: Hold takes it
	// with TakeStartDelay.
	StartDelay int
	// lateNext is set by LateNext until the next job is queued.
	lateNext bool
	// tailFresh is set from the Tail asked as a job finishes until the loop drains: a NextTick asked then joins the tail's chain.
	tailFresh bool
	afterTail []queued
	// Origin is the host async context of the code that is running (ABI section 5.4): Defer and the line capture it, and the
	// Loop makes it current again when it runs the item or job. 0 is none. Retain and Release, when set, are told of every
	// origin the line holds a job for, from the job's queuing to the end of its run.
	Origin          uint32
	Retain, Release func(origin uint32)
	// Adopted, when set, is asked as each line job finishes whether the host's Storage adopted its commit and has not answered
	// yet (Storage gate): the job's publication is then made at once, and its completion waits for Answer.
	Adopted func() bool
	// Started, when set, runs as each line job starts.
	Started func()
	// Reading is set while a Session.readOnLine job (ReadLine) runs and when it finishes: Hold asks it for the job's duration.
	Reading bool
	queue   []queued // the microtasks from qhead on; a drained queue starts over at the front of its array instead of ending up with none left
	qhead   int
	jobs    []*job // the line jobs from jhead on; a drained line starts over at the front of its array, as the queue does
	jhead   int
	ending  *job // the finished job whose completion Hold holds
	endErr  error
	running bool // a line item is queued or running
	holding bool // a finished job's completion waits for Hold's release
	parked  *job
	need    *NeedRead
	pubs    []*Publication // the finished job's publications, until Publish
	gated   *job           // a line job whose commit waits for the host's Storage: it holds the line, while other items keep running
	// Method values made once: each captures only the loop, and a method value allocates every time it is taken.
	runJobFn, completeFn, releaseFn, tailFn, quietFn func()
	// after holds what the running job asked to run at its completion, right after its publication and before the line's
	// next job: the code a caller runs on resuming from `await commitWith(...)`, which follows the commit's publication.
	after []func()

	// awaiting is set while a finished job's commit was adopted and its completion waits for the Storage's answer (Answer).
	awaiting bool
}

// Resumer is a continuation that keeps its own state: the Loop runs it as a microtask (DeferResumer) or the host after a chain of
// hops (Loop.Hops) without a closure made for each use.
type Resumer interface{ Resume() }

// queued is a microtask and the origin it was queued in, which is current again when it runs. It runs fn, or resumes r when fn is nil.
type queued struct {
	fn     func()
	r      Resumer
	origin uint32
}

type job struct {
	l      *Loop
	origin uint32       // the origin the job was queued in: the line runs it, and its completion, in it
	run    func() error // returns a *NeedRead to park, any other error is the job's own
	done   func(error)  // continuation, enqueued as a microtask after the job
	err    error        // what done receives
	// late: done resumes a tick after its microtask, as the caller of an async function that returned the job's promise does.
	late bool
	bare bool // the job takes no time of its own: Hold is not asked (a host transaction's turn on the line)
	read bool // a Session.readOnLine job: no change callback, settle or commit
	idle bool // queued while the line was idle: it starts a tick later (Begin)
	// client: done answers a host caller (ClientReadLine) and runs at Settle, a hop before the job's end.
	client  bool
	settled bool // done already ran at Settle
	// ended: the job's commit ended this runtime's task for crashes, which Resume reports before done (Runtime.Commit).
	ended *Runtime
	// rn, when set instead of run and done, is the job's own run and done (jobRunner): a struct that embeds its job holds the
	// job's state in one allocation where closures would take several (20,000 queued jobs of a task graph's commits).
	rn jobRunner
}

// jobRunner is a line job that is its own run and done.
type jobRunner interface {
	runJob() error
	doneJob(err error)
}

// hasDone reports whether the job has a done.
func (j *job) hasDone() bool { return j.done != nil || j.rn != nil }

// exec runs the job's body.
func (j *job) exec() error {
	if j.run != nil {
		return j.run()
	}
	return j.rn.runJob()
}

// finish delivers err to the job's done.
func (j *job) finish(err error) {
	if j.done != nil {
		j.done(err)
		return
	}
	j.rn.doneJob(err)
}

// Resume delivers done: in the microtask the Loop queued for it, or, for a late job, a tick after that.
func (j *job) Resume() {
	if j.late {
		j.late = false
		j.l.NextTickResumer(j)
		return
	}
	if j.ended != nil && j.err == nil {
		j.ended.ReportEnded()
	}
	j.finish(j.err)
}

// endedBy marks the running line job as the commit that ended rt's task for crashes.
func (l *Loop) endedBy(rt *Runtime) { l.jobs[l.jhead].ended = rt }

// DeferResumer queues r.Resume as a microtask.
func (l *Loop) DeferResumer(r Resumer) { l.deferItem(queued{r: r}) }

// NextTickResumer is NextTick for a continuation that keeps its own state.
func (l *Loop) NextTickResumer(r Resumer) {
	if l.tailFresh {
		l.afterTail = append(l.afterTail, queued{r: r})
		return
	}
	if l.Hops == nil || !l.Hops(1, r) {
		r.Resume()
	}
}

// NextTick runs fn one tick later: the resumption of an `await` of an async function that returned in the tick the line job it
// awaited resolved (scheduler.ts #run awaits #step, which awaits commitWith). Without a Begin hold it runs fn at once.
//
// A NextTick asked in the step whose Tail chain just started rides that chain: both resume after the same one hop, the tail's
// reaction (the line's next job) before this one, so a single hops chain carries both.
// OnComplete runs fn when the running line job completes after a successful commit: right after its publication, ahead of the
// line's next job, as the code that resumes after the awaited commit does. A job that runs again (a *NeedRead or *NeedGate) drops
// what it asked for.
func (l *Loop) OnComplete(fn func()) { l.after = append(l.after, fn) }

func (l *Loop) NextTick(fn func()) {
	if l.tailFresh {
		l.afterTail = append(l.afterTail, queued{fn: fn})
		return
	}
	if l.Begin == nil || !l.Begin(fn) {
		fn()
	}
}

// TakeStartDelay returns the start ticks the running job has not yet spent and clears them.
func (l *Loop) TakeStartDelay() int {
	d := l.StartDelay
	l.StartDelay = 0
	return d
}

// Defer queues fn as a microtask. When the array is full and at least half of it is spent front, the queued items move to the
// front instead of the array growing: the array stays proportional to what is queued, and each move is paid for by the items
// that ran since the last one.
func (l *Loop) Defer(fn func()) { l.deferItem(queued{fn: fn}) }

func (l *Loop) deferItem(q queued) {
	if l.qhead > 0 && len(l.queue) == cap(l.queue) && 2*l.qhead >= len(l.queue) {
		n := copy(l.queue, l.queue[l.qhead:])
		clear(l.queue[n:])
		l.queue, l.qhead = l.queue[:n], 0
	}
	q.origin = l.Origin
	l.queue = append(l.queue, q)
}

// QueuedOrigins calls f with the origin of every microtask still queued: a Loop parked on a host answer keeps them across steps.
func (l *Loop) QueuedOrigins(f func(origin uint32)) {
	for _, q := range l.queue[l.qhead:] {
		if q.origin != 0 {
			f(q.origin)
		}
	}
}

func (l *Loop) queued() int { return len(l.queue) - l.qhead }

// pushJob queues j on the line. Like Defer, a full array at least half spent at the front moves the waiting jobs to the front
// instead of growing: popping stays O(1) and the array stays proportional to the jobs that wait.
func (l *Loop) pushJob(j *job) {
	if l.jhead > 0 && len(l.jobs) == cap(l.jobs) && 2*l.jhead >= len(l.jobs) {
		n := copy(l.jobs, l.jobs[l.jhead:])
		clear(l.jobs[n:])
		l.jobs, l.jhead = l.jobs[:n], 0
	}
	j.origin = l.Origin
	if j.origin != 0 && l.Retain != nil {
		l.Retain(j.origin)
	}
	l.jobs = append(l.jobs, j)
}

// waiting is the number of line jobs queued and not yet started.
func (l *Loop) waiting() int { return len(l.jobs) - l.jhead }

// enqueue queues j on the line, or, when the Admit check rejects it, delivers its done with the error as a microtask. A job queued
// through LateNext is late.
func (l *Loop) enqueue(j *job) {
	j.l = l
	j.late, l.lateNext = l.lateNext, false
	if l.Admit != nil {
		if err := l.Admit(); err != nil {
			if j.hasDone() {
				j.err = err
				l.DeferResumer(j)
			}
			return
		}
	}
	l.pushJob(j)
	l.kickLine()
}

// DeferDone delivers done(err) as a microtask, a tick later when late, as the done of a job the Admit check rejected.
func (l *Loop) DeferDone(done func(error), err error, late bool) {
	l.DeferResumer(&job{l: l, done: done, err: err, late: late})
}

// LateNext makes the next job queued through a Line method late: its done runs a tick after its microtask. runtime.commit is an async
// function that returns the promise of the commit, and its caller resumes a tick after the commit's resolution.
func (l *Loop) LateNext() { l.lateNext = true }

// Line queues a job on the Session line. run is the job body; it restarts from
// scratch after a NeedRead. done receives the job's final error (nil on
// success) as a microtask.
func (l *Loop) Line(run func() error, done func(error)) {
	l.enqueue(&job{run: run, done: done})
}

// LineAfterTail is Line for a commit job queued through `#enqueue` (session.ts :530-531): on an idle line it starts a tick later (Begin),
// as `this.#tail.then(job)` does on an already resolved tail.
func (l *Loop) LineAfterTail(run func() error, done func(error)) {
	l.enqueue(&job{run: run, done: done, idle: !l.Busy()})
}

// QueueAfterTail is LineAfterTail for a job that is its own run and done: j.rn is set, and the job is queued by address.
func (l *Loop) QueueAfterTail(j *job) {
	j.idle = !l.Busy()
	l.enqueue(j)
}

// ReadLine queues a Session.readOnLine job (session.ts:105-115): a read-only job on the line, whose duration is its reads
// plus the three ticks of the return through #enqueue (hopcost.ReadOnLine).
func (l *Loop) ReadLine(run func() error, done func(error)) {
	l.enqueue(&job{run: run, done: done, read: true, idle: !l.Busy()})
}

// ClientReadLine is ReadLine for a job a host call queues and whose done answers that caller (an api_result). The caller awaits the
// `run` promise of #enqueue, which settles a hop before the line tail resolves, so Hold calls Settle a hop before the job's end and
// the tail holds the line one hop longer (session.ts :105-115, :530-537).
func (l *Loop) ClientReadLine(run func() error, done func(error)) {
	l.enqueue(&job{run: run, done: done, read: true, idle: !l.Busy(), client: true})
}

// Settle answers the finished job's host caller now when the job came through ClientReadLine: Hold calls it a hop before the
// job's end. It reports whether it answered.
func (l *Loop) Settle() bool {
	j, err := l.ending, l.endErr
	if j == nil || !j.client || j.settled || !j.hasDone() {
		return false
	}
	j.settled = true
	j.err = err
	l.DeferResumer(j)
	return true
}

// EndingClient reports whether the job whose completion Hold holds answers a host caller (ClientReadLine).
func (l *Loop) EndingClient() bool { return l.ending != nil && l.ending.client && l.ending.hasDone() }

// LineBare queues a job that only takes the line for someone else, a host transaction that holds it until Blocked reports
// false: it keeps its place in the line's order and takes no job duration of its own.
func (l *Loop) LineBare(run func() error) {
	l.pushJob(&job{run: run, bare: true})
	l.kickLine()
}

// Busy reports whether a line job is queued, running, held or waiting for the host.
func (l *Loop) Busy() bool {
	return l.waiting() != 0 || l.running || l.holding || l.parked != nil || l.gated != nil
}

// Kick starts the next line job after the holder of the line released it.
func (l *Loop) Kick() { l.kickLine() }

func (l *Loop) kickLine() {
	if l.running || l.holding || l.parked != nil || l.gated != nil || l.waiting() == 0 || (l.Blocked != nil && l.Blocked()) {
		return
	}
	l.running = true
	l.Defer(l.fns().runJobFn)
}

// fns makes the method values on first use.
func (l *Loop) fns() *Loop {
	if l.runJobFn == nil {
		l.runJobFn, l.completeFn, l.releaseFn, l.tailFn, l.quietFn = l.runJob, l.complete, l.release, l.tailDone, l.quiet
	}
	return l
}

// release ends a hold that Begin or Tail asked for: the line is free for the next job.
func (l *Loop) release() { l.holding = false; l.kickLine() }

func (l *Loop) runJob() {
	l.running = false
	if l.holding || l.parked != nil || l.gated != nil || l.waiting() == 0 || (l.Blocked != nil && l.Blocked()) {
		return
	}
	j := l.jobs[l.jhead]
	l.Origin = j.origin
	if j.idle && l.Begin != nil && !j.bare && l.Hold != nil {
		// The job runs now and its hold lasts the tick it would have waited: the job body takes no tick of its own, and the
		// hold's offsets (the statements' COMMIT, the publication, the end) are all counted from the start tick.
		j.idle = false
		l.StartDelay = 1
	}
	if j.idle && l.Begin != nil {
		j.idle = false
		l.holding = true
		if l.Begin(l.fns().releaseFn) {
			return
		}
		l.holding = false
	}
	if l.Started != nil {
		l.Started()
	}
	sess := l.Session
	if sess != nil {
		sess.holdPubs, sess.inJob = true, true
	}
	l.Reading = j.read
	l.after = l.after[:0]
	err := j.exec()
	var pubs []*Publication
	if sess != nil {
		sess.holdPubs, sess.inJob = false, false
		pubs, sess.heldPubs = sess.heldPubs, nil
	}
	if IsNeedGate(err) {
		// Nothing runs on the line until the host answers; microtasks outside the line continue, as they do in Pi while a
		// job awaits Storage.commit.
		l.gated = j
		l.after = l.after[:0]
		return
	}
	if need, ok := IsNeedRead(err); ok {
		l.parked, l.need = j, need
		l.after = l.after[:0]
		return
	}
	l.pubs = pubs
	if j.origin != 0 && l.Release != nil {
		l.Release(j.origin)
	}
	// The popped job and the closures it captured must not stay reachable through the backing array.
	l.jobs[l.jhead] = nil
	if l.jhead++; l.jhead == len(l.jobs) {
		l.jobs, l.jhead = l.jobs[:0], 0
	}
	l.ending, l.endErr = j, err
	if l.Adopted != nil && l.Adopted() {
		// The Storage adopted this job's commit and has not resolved: the commit is published now, inside the adoption, and the
		// job ends when the Storage answers (Answer), as Pi's #runCommit resumes from `await storage.commit()`.
		l.holding = true
		l.awaiting = true
		l.Publish()
		return
	}
	if l.Hold != nil && !j.bare && l.Hold(l.fns().completeFn) {
		l.holding = true
		return
	}
	if l.StartDelay > 0 {
		l.StartDelay = 0
		l.holding = true
		if l.Begin(l.fns().completeFn) {
			return
		}
		l.holding = false
	}
	l.complete()
}

// complete is the end of the job runJob finished (l.ending): its publications, its done as a microtask, the Tail hold if one is
// asked for, and the next job.
func (l *Loop) complete() {
	j, err := l.ending, l.endErr
	l.holding = false
	l.Publish()
	if err == nil {
		after := l.after
		l.after = nil
		for _, fn := range after {
			fn()
		}
	}
	l.ending, l.endErr = nil, nil
	if j.hasDone() && !j.settled {
		j.err = err
		l.DeferResumer(j)
	}
	l.Answered = j.settled
	tailed := (l.waiting() != 0 || !j.bare) && l.Tail != nil && l.Tail(l.fns().tailFn)
	l.tailFresh = tailed && !l.Answered
	l.Answered = false
	if tailed {
		l.holding = true
		return
	}
	l.kickLine()
}

// tailDone ends the hold Tail asked for: the line is free for the next job, and what waited for the tail runs.
func (l *Loop) tailDone() {
	l.tailFresh = false
	l.holding = false
	l.kickLine()
	after := l.afterTail
	l.afterTail = nil
	for _, q := range after {
		l.deferItem(q)
	}
}

// MemoryPubsOnly reports whether every publication of the finished job is a deferred commit held in memory (Publication.Memory).
func (l *Loop) MemoryPubsOnly() bool {
	for _, pub := range l.pubs {
		if !pub.Memory {
			return false
		}
	}
	return len(l.pubs) != 0
}

// HasPubs reports whether the finished job whose completion Hold holds has publications to deliver.
func (l *Loop) HasPubs() bool { return len(l.pubs) != 0 }

// QuietEnd reports whether the completion of the job whose end Hold holds has nothing to do but the tail tick Tail asks for:
// no done to run, no publication, no continuation of its own and no load to reveal. Such a job and its tail need no step of their
// own between them: the line stays held from the job's last tick to the tail's and QuietRelease frees it there.
func (l *Loop) QuietEnd() bool {
	j := l.ending
	return j != nil && !j.hasDone() && !j.bare && !j.client && l.Tail != nil && len(l.pubs) == 0 && len(l.after) == 0 &&
		(l.Session == nil || !l.Session.HasUnrevealedLoads())
}

// QuietRelease is the release of a QuietEnd job: its end and the end of its tail, which both did nothing, in one.
func (l *Loop) QuietRelease() func() { return l.fns().quietFn }

func (l *Loop) quiet() {
	l.ending, l.endErr = nil, nil
	l.holding = false
	l.kickLine()
}

// Publish delivers the finished job's publications now. Pi publishes a commit when `#runCommit` resumes from Storage.commit
// (session.ts :446-449), two ticks before the job's run resolves; Hold calls it at that point, and the job's completion
// publishes whatever is still held.
func (l *Loop) Publish() {
	pubs := l.pubs
	l.pubs = nil
	if l.Session != nil {
		l.Session.revealLoads()
	}
	for _, pub := range pubs {
		l.Session.publish(pub)
	}
}

// Answer takes the Storage's answer to the commit a finished job adopted: a failure of the call after the adoption is the job's own
// error (the commit stays published). The job ends when Completer's function runs. It reports whether a job waited.
func (l *Loop) Answer(err error) bool {
	if !l.awaiting {
		return false
	}
	l.awaiting = false
	if err != nil {
		l.endErr = err
	}
	return true
}

// Completer is the function that ends the job Answer released.
func (l *Loop) Completer() func() { return l.fns().completeFn }

// Ungate runs the job that returned a *NeedGate again, from the start, after the host's Storage answered.
func (l *Loop) Ungate() {
	if l.gated != nil {
		l.gated = nil
		l.running = true
		l.queue, l.qhead = append([]queued{{fn: l.fns().runJobFn}}, l.queue[l.qhead:]...), 0
	}
}

// Idle reports whether the Loop has nothing queued and is not parked.
func (l *Loop) Idle() bool {
	return l.queued() == 0 && l.parked == nil && l.gated == nil && l.waiting() == 0 && !l.running && !l.holding
}

// Parked returns the read request the Loop waits on, or nil.
func (l *Loop) Parked() *NeedRead {
	if l.parked == nil {
		return nil
	}
	return l.need
}

// ErrParked is returned by Run when a job waits for host reads.
// Read-only: assigned once at initialization.
var ErrParked = errors.New("loop parked on a read")

// Run processes queued items until none remain or a job parks. It returns
// ErrParked when parked; call Resume after the host served the reads.
func (l *Loop) Run() error {
	for l.parked == nil && l.queued() > 0 {
		q := l.queue[l.qhead]
		l.queue[l.qhead] = queued{} // the run item's captured data must not stay reachable through the backing array
		if l.qhead++; l.qhead == len(l.queue) {
			l.queue, l.qhead = l.queue[:0], 0
		}
		l.Origin = q.origin
		if q.r != nil {
			q.r.Resume()
		} else {
			q.fn()
		}
	}
	l.tailFresh = false
	if l.parked != nil {
		return ErrParked
	}
	return nil
}

// Resume re-runs the parked job and continues; the host must have answered its reads.
func (l *Loop) Resume() error {
	if l.parked != nil {
		l.parked, l.need = nil, nil
		l.running = true
		l.queue, l.qhead = append([]queued{{fn: l.fns().runJobFn}}, l.queue[l.qhead:]...), 0
	}
	return l.Run()
}
