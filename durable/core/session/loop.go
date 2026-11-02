package session

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
)

// Loop is the core's stand-in for the JavaScript event loop and the Session line. Continuations that Pi runs as promise reactions or
// queueMicrotask callbacks run here as queue items in FIFO order; a commit job runs to completion in one item, so jobs serialize as
// they do on Pi's mutation line.
//
// A job whose store read needs the host (*NeedRead) parks the Loop with that request: Run returns it, and after the host answered, Run
// resumes with the same job from the start. Nothing else runs while a job is parked, as nothing else runs while Pi's line awaits
// storage. A job that touches a key an unpublished commit writes (*NeedPublication) holds the line while it waits for that commit's
// publication, and then runs again from the start: the Pi callback goes on after its `await gate`.
//
// Time passes in ticks, the microtask passes of the JavaScript loop. A line job's body runs at once, when the line is free; Pi's job
// takes Elapsed ticks to the point its `work` returns (session.ts #runCommit's admission of a commit to the writer, or its return), and
// the rest of the line's life of the job follows from the Session's own promise structure (hopcost): the line is free LineRelease ticks
// after the return, the writer stores the parts it admitted (StoreTicks) and publishes them in line order a macrotask apart, and each
// job's outcome is delivered in line order (the fences of session.ts #enqueue). Every wait is Wait: the host's Hops chain, or, without
// one, that many items of the microtask queue.
type Loop struct {
	// Blocked reports that something outside the Loop holds the Session line (a host transaction, ABI section 8): queued line
	// jobs wait until it reports false and Kick runs them.
	Blocked func() bool
	// Admit, when set, is the admission check of a line job as it is enqueued (session.ts #assertUsable): a job it
	// rejects never runs, and its done receives the error as a microtask.
	Admit func() error
	// Elapsed, when set, is asked as each line job's body ends for Pi's duration of the job, in ticks from its start on the line: to the
	// resumption of `await change(tx)`, where the Session is checked healthy (callback), and to the return of its work (work): the
	// change callback's awaits and the settle's, for a commit (hopcost.Job.Return); a read job's (hopcost.ReadReturn). Without it the
	// work returns at once; a callback of 0 is not checked.
	Elapsed func(e Ended) (callback, work int)
	// StoreTicks, when set, is asked as the writer calls the Storage for the batches it sends, and returns the ticks from the call to the
	// Storage's read of its ID counter (hopcost.IDReadLead before the receipts; the ID a job mints until then is in the next ID the call
	// stores), to the Storage's receipts (the Loop commits the batches to the Session's Store then) and to the writer's resumption from
	// `await storage.commit(...)` (hopcost.StoreCallResume). Without it the Storage answers in the tick of the call. A
	// Session.CommitBatches takes the call instead.
	StoreTicks func(batches [][]Write) (read, receive, resume int)
	// Hops, when set, parks r on a chain of n hops and returns true; r.Resume runs once the chain ran, at once in the host's hopped step.
	// The host keeps r itself, not a closure over it, so a continuation that carries its own state (a job, an invocation) costs the
	// host no allocation.
	Hops func(n int, r Resumer) bool
	// Macro, when set, takes a macrotask (`setImmediate`): it runs r.Resume after the microtask queue drained, and reports true. Without
	// it the Loop runs it itself when Run has nothing else to run.
	Macro func(r Resumer) bool
	// Started, when set, runs as each line job starts.
	Started func()
	// Origin is the host async context of the code that is running (ABI section 5.4): Defer and the line capture it, and the
	// Loop makes it current again when it runs the item or job. 0 is none. Retain and Release, when set, are told of every
	// origin the line holds a job for, from the job's queuing to the end of its run.
	Origin          uint32
	Retain, Release func(origin uint32)

	queue   []queued // the microtasks from qhead on; a drained queue starts over at the front of its array instead of ending up with none left
	qhead   int
	jobs    []*job // the line jobs from jhead on; a drained line starts over at the front of its array, as the queue does
	jhead   int
	macros  []Resumer
	running bool // a line item is queued or running
	holding bool // the line is held: a job's work has not returned or been released, or a job waits for room
	parked  *job
	need    *NeedRead
	gated   *job // a line job that waits for a publication: it holds the line, while other items keep running
	host    *job // a host job (LineHost) whose callback runs in the host: it holds the line until End
	cur     *job // the job on the line, from its start to the return of its work
	// Method values made once: each captures only the loop, and a method value allocates every time it is taken.
	runJobFn func()
	// after holds what the running job asked to run when its outcome is delivered, right before its caller resumes: the code a caller runs
	// on resuming from `await commitWith(...)`.
	after []func()

	// nextIndex is the line index of the next job, outstanding the jobs that started and have no outcome delivered yet, roomWait whether the
	// line waits for room to start one (session.ts MAX_OUTSTANDING), lastDelivered the line index of the last outcome delivered through a fence.
	nextIndex     int
	outstanding   int
	roomWait      bool
	lastDelivered int
	// fenceTail is the job whose fence the next job's chains on (session.ts #fence).
	fenceTail *job
	// runs are the runs of jobs that ended, for the jobs that start.
	runs *jobRun
	// sess is the Session whose commits the line jobs make (Bind): the Loop admits the part a job made to the Session's writer when the job's
	// work returns, and delivers the job's outcome when the part is published.
	sess *Session
	// lateNext is set by LateNext until the next job is queued.
	lateNext bool
}

// Ended describes a line job whose body ended, for Elapsed.
type Ended struct {
	// Part is the part the job admitted to the writer: stored (Writes), or held in memory (none); nil when it admitted none.
	Part *Part
	// Err is the error the body returned, nil for success.
	Err error
	// Read is set for a Session.readOnLine job (ReadLine): no change callback, settle or commit.
	Read bool
	// Ticks is what the job's transaction and settle charged in Pi's await ticks, for a host that counts no more of them than the Session does.
	Ticks JobTicks
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

type stageKind uint8

// The steps of a line job after its body ran, each a wait of the Loop.
const (
	stageCheck    stageKind = iota // the job's callback resumed: the Session is checked healthy
	stageReturn                    // the job's work returned (admission of its part)
	stageFree                      // the line is free for the next job
	stageFloor                     // the job's outcome settled at the earliest tick it can
	stagePub                       // the job's part was published, or never will be, and its outcome settled
	stageMature                    // the job's fence settled long enough ago for the next job's to follow
	stageDeliver                   // the job's outcome is delivered (session.ts #delivered)
	stageDone                      // the job's caller resumes
	stageRoom                      // a job that waited for room starts
	stageHit                       // the job reached the touch it waits at
	stageGate                      // the key's writer was published, and the job's continuation is due
	stageAnswer                    // a host job's caller is answered ahead of its resumption (LineHostLead)
	stageGateOpen                  // the gate the job waits at opened
	stageOutcome                   // the outcome of the job's part settled
	stageCount
)

// stage is one pending step of a job. Without a host's hops its wait counts the microtask passes left in the stage itself.
type stage struct {
	j    *job
	k    stageKind
	left int
}

func (s *stage) Resume() {
	if s.left > 0 {
		if s.left--; s.left > 0 {
			s.j.l.DeferResumer(s)
			return
		}
	}
	s.j.l.advance(s.j, s.k)
}

// waitStage runs step st n ticks from now.
func (l *Loop) waitStage(n int, st *stage) {
	if n <= 0 {
		l.advance(st.j, st.k)
		return
	}
	if l.Hops != nil && l.Hops(n, st) {
		return
	}
	st.left = n
	l.DeferResumer(st)
}

type job struct {
	l      *Loop
	origin uint32 // the origin the job was queued in: the line runs it, and its completion, in it
	// lead is how many ticks before its caller resumes a host job's done is called (LineHostLead); at most hopcost.Delivery.
	lead uint8
	// matured: the job's fence settled long enough ago for the next job's to follow; the job after it reads it whether or not the job still has its run.
	matured bool
	run     func() error // returns a *NeedRead to park, any other error is the job's own
	done    func(error)  // continuation, run when the job's outcome is delivered
	err     error        // what done receives
	// late: done resumes a tick after its delivery, as the caller of an async function that returned the job's promise does.
	late bool
	bare bool // the job takes no time of its own: no Elapsed, no outcome (a host transaction's turn on the line)
	read bool // a Session.readOnLine job: no change callback, settle or commit
	idle bool // queued while the line was idle: it starts a tick later
	// maintain is a job of the Session's own upkeep: no outcome to deliver, so no fence, no count against MaxOutstanding.
	maintain bool
	// exempt is a scheduler-internal job whose outcome goes only to scheduler code (session.ts commitInternal): when it admitted no
	// part and waited at no gate, its outcome is delivered as soon as it has one.
	exempt bool
	// host: the job's callback runs in the host (LineHost): its body only takes the line, and End ends it.
	host bool
	// ended: the job's commit ended this runtime's task for crashes, which Resume reports before done (Runtime.Commit).
	ended *Runtime
	// rn, when set instead of run and done, is the job's own run and done (LineRunner): a struct that embeds its job holds the job's state
	// in one allocation where closures would take several (20,000 queued jobs of a task graph's commits).
	rn LineRunner

	index   int  // the line index: outcomes are delivered in this order
	started bool // the job counts against MaxOutstanding
	hj      HostJob
	// jobRun is what the job keeps from its start to its end, taken from the Loop's free runs then: a queued job holds none of it, and a task
	// graph queues 20,000 at once.
	*jobRun
}

// jobRun is the state of a job that started: its runs, its fence and its pending steps.
type jobRun struct {
	hit bool // a touch of the job waited at a gate
	ran bool // the job's body ran before: the body now running is a restart (NeedRead, NeedPublication)
	// answered is set once a host job's caller was answered ahead of its resumption.
	answered bool
	// consumed is the ticks of the job's duration that passed before the touch it last waited at: Pi's callback ran them while it waited.
	consumed  int
	start     int // the ticks the job waited for its start on an idle line, where the host runs its body at once
	pending   int // the events the job's wait for a publication still needs
	waitAt    int
	gateExtra int       // the ticks past GateResume the gate's waiter resumes the job after (NeedPublication.Extra)
	ends      []Resumer // resumed when the job's outcome settles (OnJobEnd)
	rest      int       // the ticks from the check of the job's callback to the return of its work
	part      *Part
	after     []func()
	// The job's fence: aFloor and aPub are set once its outcome settled, bOK once the fence before it matured.
	aFloor, aPub, bOK, fired, free bool
	prev, next                     *job
	// open is the steps of stg issued and not yet run; complete is set once the job's caller resumed: the run ends when both are done and the fence has fired.
	open     int
	complete bool
	stg      [stageCount]stage
	// spare links the runs of ended jobs.
	spare *jobRun
}

// LineRunner is a line job that is its own run and done: a struct holds the job's state in one allocation where closures would take several.
type LineRunner interface {
	RunJob() error
	DoneJob(err error)
}

// hasDone reports whether the job has a done.
func (j *job) hasDone() bool { return j.done != nil || j.rn != nil }

// exec runs the job's body.
func (j *job) exec() error {
	var err error
	if j.run != nil {
		err = j.run()
	} else {
		err = j.rn.RunJob()
	}
	j.ran = true
	return err
}

// finish delivers err to the job's done.
func (j *job) finish(err error) {
	if j.done != nil {
		j.done(err)
		return
	}
	j.rn.DoneJob(err)
}

// Resume delivers done: when the job's outcome is delivered, or, for a late job, a tick after that.
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
func (l *Loop) endedBy(rt *Runtime) { l.cur.ended = rt }

// stage is the pending step k of j.
func (j *job) stage(k stageKind) *stage {
	j.open++
	st := &j.stg[k]
	st.j, st.k = j, k
	return st
}

// Bind makes s the Session whose commits the line jobs of l make: l admits the part a job made to s's writer when the job's work returns, and
// delivers the job's outcome when the part is published; s's writer waits in l's ticks.
func (l *Loop) Bind(s *Session) {
	l.sess, s.loop = s, l
}

// OnJobEnd calls end once the outcome of the job on the line settles (its commit published, or never to be); at once off the line.
func (l *Loop) OnJobEnd(end Resumer) {
	if l.cur == nil {
		end.Resume()
		return
	}
	l.cur.ends = append(l.cur.ends, end)
}

func (j *job) runEnds() {
	ends := j.ends
	j.ends = nil
	for _, end := range ends {
		end.Resume()
	}
}

// DeferResumer queues r.Resume as a microtask.
func (l *Loop) DeferResumer(r Resumer) { l.deferItem(queued{r: r}) }

// tickChain waits n microtask passes for the Loop without a host's hops: each pass queues the next.
type tickChain struct {
	l    *Loop
	left int
	r    Resumer
}

func (c *tickChain) Resume() {
	if c.left--; c.left == 0 {
		c.r.Resume()
		return
	}
	c.l.DeferResumer(c)
}

// Wait runs r.Resume n ticks from now: after a chain of the host's hops, or, without one, n microtask passes; at once for n <= 0.
func (l *Loop) Wait(n int, r Resumer) {
	if n <= 0 {
		r.Resume()
		return
	}
	if l.Hops != nil && l.Hops(n, r) {
		return
	}
	l.DeferResumer(&tickChain{l: l, left: n, r: r})
}

// Macrotask runs r.Resume once the microtask queue drained and the macrotasks queued before it ran (`setImmediate`).
func (l *Loop) Macrotask(r Resumer) {
	if l.Macro != nil && l.Macro(r) {
		return
	}
	l.macros = append(l.macros, r)
}

// NextTickResumer is NextTick for a continuation that keeps its own state.
func (l *Loop) NextTickResumer(r Resumer) {
	if l.Hops == nil || !l.Hops(1, r) {
		r.Resume()
	}
}

// NextTick runs fn one tick later: the resumption of an `await` of an async function that returned in the tick the line job it awaited
// resolved (scheduler.ts #run awaits #step, which awaits commitWith). Without a host's hops it runs fn at once.
func (l *Loop) NextTick(fn func()) {
	if l.Hops == nil || !l.Hops(1, funcResumer(fn)) {
		fn()
	}
}

type funcResumer func()

func (f funcResumer) Resume() { f() }

// OnComplete runs fn when the running line job's outcome is delivered after a successful commit, right before its caller resumes, as the
// code that resumes after the awaited commit does. A job that runs again (a *NeedRead or *NeedPublication) drops what it asked for.
func (l *Loop) OnComplete(fn func()) { l.after = append(l.after, fn) }

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

// newJob is a line job that runs run and delivers its outcome to done. It is the one place a job is allocated.
func (l *Loop) newJob(run func() error, done func(error)) *job {
	return &job{l: l, run: run, done: done}
}

// enqueue queues j on the line, or, when the Admit check rejects a job that asserts the Session usable, delivers its done with the error as a microtask. A job queued
// through LateNext is late.
func (l *Loop) enqueue(j *job) {
	j.l = l
	j.late, l.lateNext = l.lateNext, false
	// A maintain job is the Session's own upkeep (session.ts #maintain): it asserts nothing, so it runs after the seal.
	if l.Admit != nil && !j.maintain {
		if err := l.Admit(); err != nil {
			if j.hasDone() {
				j.err = err
				l.DeferResumer(j)
			}
			return
		}
	}
	j.index = l.nextIndex
	l.nextIndex++
	l.pushJob(j)
	l.kickLine()
}

// DeferDone delivers done(err) as a microtask, a tick later when late, as the done of a job the Admit check rejected.
func (l *Loop) DeferDone(done func(error), err error, late bool) {
	j := l.newJob(nil, done)
	j.err, j.late = err, late
	l.DeferResumer(j)
}

// LateNext makes the next job queued through a Line method late: its done runs a tick after its delivery. runtime.commit is an async
// function that returns the promise of the commit, and its caller resumes a tick after the commit's resolution.
func (l *Loop) LateNext() { l.lateNext = true }

// Line queues a job on the Session line. run is the job body; it restarts from
// scratch after a NeedRead or a NeedPublication. done receives the job's final error (nil on
// success) when its outcome is delivered.
func (l *Loop) Line(run func() error, done func(error)) {
	l.enqueue(l.newJob(run, done))
}

// LineInternal is Line for a job of the scheduler's own whose outcome goes only to scheduler code (session.ts commitInternal): when it
// admits no part and waits at no gate, its outcome is delivered as soon as it has one, ahead of the outcomes of the jobs before it.
func (l *Loop) LineInternal(run func() error, done func(error)) {
	j := l.newJob(run, done)
	j.exempt, j.idle = true, !l.Busy()
	l.enqueue(j)
}

// LineMaintain queues a job of the Session's own upkeep on the line (session.ts #maintain): it has no outcome to deliver, so no fence.
func (l *Loop) LineMaintain(run func() error) {
	j := l.newJob(run, nil)
	j.maintain, j.idle = true, !l.Busy()
	l.enqueue(j)
}

// LineAfterTail is Line for a commit job queued through `#enqueue` (session.ts :530-531): on an idle line it starts a tick later,
// as `this.#tail.then(job)` does on an already resolved tail.
func (l *Loop) LineAfterTail(run func() error, done func(error)) {
	j := l.newJob(run, done)
	j.idle = !l.Busy()
	l.enqueue(j)
}

// LineRunnerAfterTail is LineAfterTail for a job that is its own run and done: r and the job are the two allocations of the queued job.
func (l *Loop) LineRunnerAfterTail(r LineRunner) {
	j := l.newJob(nil, nil)
	j.rn, j.idle = r, !l.Busy()
	l.enqueue(j)
}

// QueueAfterTail is LineAfterTail for a job that is its own run and done: j.rn is set, and the job is queued by address.
func (l *Loop) QueueAfterTail(j *job) {
	j.idle = !l.Busy()
	l.enqueue(j)
}

// QueueInternal is QueueAfterTail for a job of the scheduler's own whose outcome goes only to scheduler code (LineInternal).
func (l *Loop) QueueInternal(j *job) {
	j.exempt = true
	l.QueueAfterTail(j)
}

// ReadLine queues a Session.readOnLine job (session.ts:105-115): a read-only job on the line, whose duration is its reads (Elapsed).
func (l *Loop) ReadLine(run func() error, done func(error)) {
	j := l.newJob(run, done)
	j.read, j.idle = true, !l.Busy()
	l.enqueue(j)
}

// ReadLineLead is ReadLine whose done is called lead ticks (at most Delivery) before the tick its caller resumes: the host answers its caller's
// promise ahead of the caller's own resumption (a facade that lags the answer by lead ticks), as LineHostLead does for a transaction.
func (l *Loop) ReadLineLead(run func() error, done func(error), lead int) {
	j := l.newJob(run, done)
	j.read, j.idle, j.lead = true, !l.Busy(), uint8(min(max(lead, 0), hopcost.Delivery))
	l.enqueue(j)
}

// ErrHostNotGranted is what the grant of a host job returns when it takes nothing (LineHost).
// Read-only: assigned once at initialization.
var ErrHostNotGranted = errors.New("host job not granted")

// HostJob is a line job whose change callback runs in the host, one operation per event (ABI section 8): the host's transaction of Pi's
// `session.commit(async (tx) => ...)`. It takes the line like any job; the host drives the transaction (Session.Begin, its operations, Session.Finish
// or Abandon), and End hands the Loop the end of the callback: the rest of the job (the settle, the part's admission, the line's release and
// the delivery of its outcome) follows as for a job whose body ran in the core, from the ticks the transaction charged.
type HostJob struct{ j *job }

// LineHost queues a host job: grant runs when it takes the line (the host's callback may begin), and an error it returns ends the job at once
// (the Session is closed or failed, or the caller's signal was aborted). done receives the job's outcome, once it is delivered. grant returns
// ErrHostNotGranted for a transaction the host abandoned while it waited: the job takes nothing and has no outcome.
func (l *Loop) LineHost(grant func() error, done func(error)) *HostJob {
	return l.LineHostLead(grant, done, 0)
}

// LineHostLead is LineHost whose done is called lead ticks (at most Delivery) before the tick its caller resumes: the host answers its
// caller's promise ahead of the caller's own resumption (a facade that lags the answer by lead ticks). The outcome is still delivered, and
// counted, at its own tick; a failure that poisons the job is decided when done is called.
func (l *Loop) LineHostLead(grant func() error, done func(error), lead int) *HostJob {
	j := l.newJob(grant, done)
	j.host, j.lead = true, uint8(min(max(lead, 0), hopcost.Delivery))
	j.hj.j = j
	l.enqueue(j)
	return &j.hj
}

// End is the end of the host's callback: err is what it threw or its commit failed with, nil when it succeeded and Session.Finish admitted
// what it staged. It must be called once the job took the line (grant ran).
func (h *HostJob) End(err error) {
	j := h.j
	l := j.l
	if l.host != j {
		return
	}
	l.host = nil
	if l.sess != nil {
		l.sess.inJob = false
	}
	l.finishBody(j, err)
}

// LineBare queues a job that only takes the line for someone else, a host transaction that holds it until Blocked reports
// false: it keeps its place in the line's order and takes no job duration of its own.
func (l *Loop) LineBare(run func() error) {
	j := l.newJob(run, nil)
	j.bare = true
	l.pushJob(j)
	l.kickLine()
}

// Busy reports whether a line job is queued, running, held or waiting for the host or for a publication.
func (l *Loop) Busy() bool {
	return l.waiting() != 0 || l.running || l.holding || l.parked != nil || l.gated != nil || l.host != nil
}

// Kick starts the next line job after the holder of the line released it.
func (l *Loop) Kick() { l.kickLine() }

// kickLine starts the next job in a microtask of its own: the line was idle when it was queued.
func (l *Loop) kickLine() {
	if l.running || l.holding || l.parked != nil || l.gated != nil || l.host != nil || l.waiting() == 0 || (l.Blocked != nil && l.Blocked()) {
		return
	}
	l.running = true
	if l.runJobFn == nil {
		l.runJobFn = l.runJob
	}
	l.Defer(l.runJobFn)
}

// Restarted reports whether the body of the running job is running again, after a *NeedRead or a *NeedPublication ended an earlier run: what
// Pi checks once when a job starts (session.ts #runCommit throwIfAborted) is not checked again.
func (l *Loop) Restarted() bool { return l.cur != nil && l.cur.ran }

// Consumed is the ticks of the running job's duration that passed before the touch it last waited at, in earlier runs of its body: Pi's callback ran
// them while it waited, so ticks the body schedules at an offset of the job's duration come that much earlier.
func (l *Loop) Consumed() int {
	if l.cur == nil {
		return 0
	}
	return l.cur.consumed
}

// Until is the ticks from now to the tick d of the running job's duration. The body of a job runs at once, in the tick the job starts, which for a
// job that starts on an idle line is a tick before Pi's (the job's start); a body that runs again after a touch has spent the ticks the
// earlier run waited (Consumed).
func (l *Loop) Until(d int) int {
	if l.cur == nil {
		return d
	}
	if l.cur.consumed > 0 {
		return d - l.cur.consumed
	}
	return d + l.cur.start
}

// boundary is the line index of the job on the line, else of the last outcome delivered: where a failure that no job caused begins.
func (l *Loop) boundary() int {
	if l.cur != nil {
		return l.cur.index
	}
	return l.lastDelivered
}

// hit notes that a touch of the job on the line waited at a gate.
func (l *Loop) hit() {
	if l.cur != nil {
		l.cur.hit = true
	}
}

func (l *Loop) runJob() {
	l.running = false
	if l.holding || l.parked != nil || l.gated != nil || l.host != nil || l.waiting() == 0 || (l.Blocked != nil && l.Blocked()) {
		return
	}
	j := l.jobs[l.jhead]
	if !j.started && !j.bare && !j.maintain && l.outstanding >= hopcost.MaxOutstanding {
		// Beyond MaxOutstanding the line waits for the outcome of a job to be delivered.
		l.roomWait, l.holding = true, true
		return
	}
	l.begin(j)
}

// begin runs the body of the job at the head of the line.
func (l *Loop) begin(j *job) {
	l.Origin = j.origin
	if !j.started {
		j.started = true
		l.takeRun(j)
		if !j.maintain && !j.bare {
			l.outstanding++
		}
		if j.idle {
			// Pi starts it a tick after the call; a host that runs the body at once adds that tick to the job's duration.
			j.idle = false
			if l.Hops != nil && !j.bare {
				j.start = 1
			}
		}
	}
	l.cur = j
	if l.Started != nil {
		l.Started()
	}
	sess := l.sess
	if sess != nil {
		sess.loop = l
		sess.inJob, sess.touchAt, sess.part = true, 0, nil
	}
	l.after = l.after[:0]
	err := j.exec()
	if j.host && errors.Is(err, ErrHostNotGranted) {
		// The host's transaction was abandoned while it waited: it took nothing, and has no outcome.
		if sess != nil {
			sess.inJob = false
		}
		l.cur = nil
		l.outstanding--
		l.freeRun(j, j.jobRun)
		l.jobs[l.jhead] = nil
		if l.jhead++; l.jhead == len(l.jobs) {
			l.jobs, l.jhead = l.jobs[:0], 0
		}
		if j.origin != 0 && l.Release != nil {
			l.Release(j.origin)
		}
		l.startNext()
		return
	}
	if j.host && err == nil {
		// The host runs the callback: the line stays held until End, and what the transaction stages is the job's part (Session.admit).
		l.host = j
		l.after = l.after[:0]
		return
	}
	if sess != nil {
		sess.inJob = false
	}
	if np, ok := needPublication(err); ok {
		// Nothing runs on the line until the key's writer is published; microtasks outside the line continue, as they do in Pi while a job
		// awaits a gate.
		l.gated = j
		l.after = l.after[:0]
		if sess != nil {
			sess.part = nil
		}
		l.waitGate(j, np)
		return
	}
	if need, ok := IsNeedRead(err); ok {
		l.parked, l.need = j, need
		l.after = l.after[:0]
		return
	}
	l.finishBody(j, err)
}

// needPublication returns the *NeedPublication err is or wraps.
func needPublication(err error) (*NeedPublication, bool) {
	if err == nil {
		return nil, false
	}
	return errors.AsType[*NeedPublication](err)
}

// waitGate parks the job at the touch it made: it goes on once the key's writer is published (the touch's `gate.then(assertHealthy)` and the
// await of it), and the ticks Pi's callback spent before the touch passed.
func (l *Loop) waitGate(j *job, np *NeedPublication) {
	j.waitAt = np.At
	j.pending = 1
	if remaining := np.At - j.consumed; remaining > 0 {
		j.pending = 2
		l.waitStage(remaining, j.stage(stageHit))
	}
	j.gateExtra = np.Extra
	np.Gate.WaitResumer(j.stage(stageGateOpen))
}

// gateEvent counts one of the events a job waits for at a touch, and runs the job again from its start when it has them all.
func (l *Loop) gateEvent(j *job) {
	if j.pending--; j.pending > 0 {
		return
	}
	j.consumed = j.waitAt
	l.gated = nil
	l.begin(j)
}

// finishBody is the end of the body of the job at the head of the line: the job leaves the queue, and its work returns after Pi's duration.
func (l *Loop) finishBody(j *job, err error) {
	if j.origin != 0 && l.Release != nil {
		l.Release(j.origin)
	}
	// The popped job and the closures it captured must not stay reachable through the backing array.
	l.jobs[l.jhead] = nil
	if l.jhead++; l.jhead == len(l.jobs) {
		l.jobs, l.jhead = l.jobs[:0], 0
	}
	if j.bare {
		l.cur = nil
		l.freeRun(j, j.jobRun)
		if j.hasDone() {
			j.err = err
			l.DeferResumer(j)
		}
		l.kickLine()
		return
	}
	if sess := l.sess; sess != nil {
		j.part = sess.takePart()
	}
	if len(l.after) > 0 {
		j.after = append(j.after[:0], l.after...)
	}
	j.err = err
	callback, work := 0, 0
	var ticks JobTicks
	if sess := l.sess; sess != nil {
		ticks = sess.takeJobTicks()
	}
	if l.Elapsed != nil {
		callback, work = l.Elapsed(Ended{Part: j.part, Err: err, Read: j.read, Ticks: ticks})
	}
	callback, work = max(callback+j.start-j.consumed, 0), max(work+j.start-j.consumed, 0)
	l.holding = true
	if callback > 0 && err == nil && l.sess != nil {
		// Pi checks the Session healthy when the change callback resumes: a failure since the job started makes it fail like its callback would
		// have (`catch`: settleFailure, then the rethrow), and its commit is never admitted.
		j.rest = max(work-callback, 0)
		l.waitStage(callback, j.stage(stageCheck))
		return
	}
	l.waitStage(work, j.stage(stageReturn))
}

// checked is the check of the Session's health when the job's callback resumed (session.ts #runCommit :1092).
func (l *Loop) checked(j *job) {
	if sess := l.sess; sess.failure != nil {
		j.err = sess.writer.sessionFailed(sess.failure)
		if j.part != nil {
			j.part.discard()
		}
		j.part, j.after = nil, nil
		l.waitStage(hopcost.FailedCallbackReturn, j.stage(stageReturn))
		return
	}
	l.waitStage(j.rest, j.stage(stageReturn))
}

// advance runs step k of job j, and ends the job's run when it was the last one.
func (l *Loop) advance(j *job, k stageKind) {
	run := j.jobRun
	l.advanceStage(j, k)
	if run.open--; run.open == 0 && run.complete && (run.fired || j.maintain) {
		l.freeRun(j, run)
	}
}

// takeRun gives j the run of a job that starts.
func (l *Loop) takeRun(j *job) {
	if run := l.runs; run != nil {
		l.runs, run.spare = run.spare, nil
		j.jobRun = run
		return
	}
	j.jobRun = new(jobRun)
}

// freeRun takes run from j, which has no step left, for the next job that starts.
func (l *Loop) freeRun(j *job, run *jobRun) {
	j.jobRun = nil
	*run = jobRun{spare: l.runs}
	l.runs = run
}

// advanceStage runs step k of job j.
func (l *Loop) advanceStage(j *job, k stageKind) {
	switch k {
	case stageCheck:
		l.checked(j)
	case stageReturn:
		l.workReturned(j)
	case stageFree:
		l.holding = false
		l.startNext()
	case stageFloor:
		j.aFloor = true
		l.fire(j)
	case stagePub:
		j.aPub = true
		l.fire(j)
	case stageMature:
		j.matured = true
		if next := j.next; next != nil {
			j.next = nil
			next.bOK, next.prev = true, nil
			l.fire(next)
		}
	case stageDeliver:
		l.delivered(j)
	case stageDone:
		l.complete(j)
	case stageAnswer:
		j.answered = true
		l.poison(j)
		l.complete(j)
	case stageRoom:
		l.holding = false
		l.startNext()
	case stageHit, stageGate:
		l.gateEvent(j)
	case stageGateOpen:
		l.waitStage(hopcost.GateResume+j.gateExtra, j.stage(stageGate))
	case stageOutcome:
		if err := j.part.err; err != nil {
			j.err = err
		}
		l.waitStage(hopcost.OutcomeAfterPublication+hopcost.FenceAfterOutcome, j.stage(stagePub))
	}
}

// startNext starts the next queued job in the tick the line became free, where Pi's callback runs.
func (l *Loop) startNext() {
	if l.running || l.holding || l.parked != nil || l.gated != nil || l.host != nil || l.waiting() == 0 || (l.Blocked != nil && l.Blocked()) {
		return
	}
	l.runJob()
}

// workReturned is the return of a job's work (session.ts #runCommit): the part it made is admitted to the writer, the line is free
// LineRelease ticks later, and the job's outcome is settled and delivered in line order.
func (l *Loop) workReturned(j *job) {
	l.cur = nil
	sess := l.sess
	if sess != nil {
		sess.revealLoads()
		if j.part != nil {
			sess.writer.Admit(j.part)
			sess.sweepDocuments()
		}
	}
	l.waitStage(hopcost.LineRelease, j.stage(stageFree))
	if j.maintain {
		j.runEnds()
		j.complete = true
		return
	}
	// The fence of the job before: its own settles one tick after the outcome (session.ts `this.#fence = fence.then(() => outcome)`).
	j.prev = l.fenceTail
	l.fenceTail = j
	if j.prev == nil || j.prev.matured {
		j.bOK, j.prev = true, nil
	} else {
		j.prev.next = j
	}
	if p := j.part; p != nil {
		// The outcome follows the part's: its own earliest tick is the job's return through `ran.then(...)`.
		l.waitStage(hopcost.OutcomeSettledAfterPart+hopcost.FenceAfterOutcome, j.stage(stageFloor))
		p.OnOutcome(j.stage(stageOutcome))
	} else {
		j.aPub = true
		l.waitStage(hopcost.OutcomeSettled+hopcost.FenceAfterOutcome, j.stage(stageFloor))
		if j.exempt && !j.hit {
			// Nothing the job did waits for a publication or for an outcome before it: it is delivered as soon as it has its own.
			j.free = true
			l.waitStage(hopcost.OutcomeSettled+hopcost.FenceAfterOutcome, j.stage(stageDeliver))
		}
	}
}

// fire is the settlement of a job's fence once its outcome and the fence before it have: the next job's fence follows FenceStep ticks later, and
// the job is delivered (session.ts #enqueue).
func (l *Loop) fire(j *job) {
	if j.fired || !j.aFloor || !j.aPub || !j.bOK {
		return
	}
	j.fired = true
	j.runEnds()
	l.waitStage(hopcost.FenceStep, j.stage(stageMature))
	if !j.free {
		if j.lead > 0 {
			l.waitStage(hopcost.Delivery-int(j.lead), j.stage(stageAnswer))
		}
		l.waitStage(1, j.stage(stageDeliver))
	}
}

// delivered is `#delivered` and the poison check of the job's outcome: the count of outstanding jobs, the room a waiting job needs, and
// the error a failure that began before the job makes its outcome.
func (l *Loop) delivered(j *job) {
	if !j.free {
		l.lastDelivered = j.index
	}
	l.outstanding--
	if l.roomWait && l.outstanding < hopcost.MaxOutstanding {
		l.roomWait = false
		l.waitStage(hopcost.BackpressureResume, j.stage(stageRoom))
	}
	l.poison(j)
	if j.answered {
		return
	}
	wait := hopcost.Delivery - 1
	if j.free || (j.exempt && j.part != nil) {
		wait++ // the exempt wrapper adopts the promise of the delivery
	}
	l.waitStage(wait, j.stage(stageDone))
}

// poison makes the failure that began before the job its outcome.
func (l *Loop) poison(j *job) {
	if sess := l.sess; sess != nil && sess.poisoned(j.index) {
		j.err = sess.writer.sessionFailed(sess.failure)
	}
}

// complete is the end of a job: what its caller runs on resuming, then the caller.
func (l *Loop) complete(j *job) {
	j.complete = true
	if j.err == nil {
		after := j.after
		j.after = nil
		for _, fn := range after {
			fn()
		}
	}
	if j.hasDone() {
		j.Resume()
	}
}

// Idle reports whether the Loop has nothing queued and is not parked, and every outcome is delivered.
func (l *Loop) Idle() bool {
	return l.queued() == 0 && l.parked == nil && l.gated == nil && l.host == nil && l.waiting() == 0 && !l.running && !l.holding && l.outstanding == 0 && len(l.macros) == 0
}

// Outstanding is the number of jobs that started and have no outcome delivered yet (session.ts lineSizes.outstanding).
func (l *Loop) Outstanding() int { return l.outstanding }

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

// Run processes queued items until none remain: the microtask queue first, then, when it is empty, the macrotasks one at a time (a
// Macro hook takes them instead). A job parked on a host read holds the line only: what runs outside the line (a delivery of an earlier
// outcome, the writer) goes on, as it does in Pi while the job awaits its read. It returns ErrParked when a job is parked; call Resume
// after the host served the reads.
func (l *Loop) Run() error {
	for {
		if l.queued() > 0 {
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
			continue
		}
		if len(l.macros) == 0 {
			break
		}
		r := l.macros[0]
		l.macros[0] = nil
		l.macros = l.macros[1:]
		r.Resume()
	}
	if l.parked != nil {
		return ErrParked
	}
	return nil
}

// Resume re-runs the parked job and continues; the host must have answered its reads.
func (l *Loop) Resume() error {
	if l.parked != nil {
		j := l.parked
		l.parked, l.need = nil, nil
		l.begin(j)
	}
	return l.Run()
}
