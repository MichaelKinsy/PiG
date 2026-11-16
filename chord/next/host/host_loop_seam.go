package host

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/internal/loopseam"
)

func init() {
	loopseam.Quiesce = func(loop any) {
		if l, ok := loop.(*Loop); ok {
			l.quiesce()
		}
	}
}

// joinStall is how long a join waits for a running task before it reports the task as stalled (a Loop's stall overrides it): a
// fault detector for a task that blocks on something the loop does not run without saying so (never what the join waits
// with: the wait ends by the loop's count falling to zero). Generous: a slow store under the race detector is no stall.
const joinStall = 4 * time.Second

// What `delivered()` joins (a test's Pi `setTimeout(0)`, test/support.ts:138) is what Pi's event loop runs before a timer: every
// microtask chain queued so far, to its next wait for something outside. Two kinds of Go goroutine are links of such chains
// and are counted in hops: a hop (joined: the goroutine that runs a microtask queued from outside a section) and a task (task:
// the goroutine of an `await work()` of work that blocks, awaitTask). A task that waits for something the loop does not run
// (a gate a test resolves, a native's work still running, a timer) is parked: it is not counted until its wait is over.

func (l *Loop) hopStartLocked() {
	if l.hops == 0 {
		l.quiet = make(chan struct{})
	}
	l.hops++
}

func (l *Loop) hopEndLocked() {
	l.hops--
	if l.hops == 0 {
		releaseQuiet(&l.quiet)
	}
}

// releaseQuiet closes the count's channel and takes it out of its owner, so what is closed is never closed again: the owner
// makes the next one when its count rises from zero (hopStartLocked, runningLocked). Called with the lock of the count held.
func releaseQuiet(quiet *chan struct{}) {
	if *quiet != nil {
		close(*quiet)
		*quiet = nil
	}
}

// joined is Go for a hop that is a link of a chain Pi runs as microtasks, never a wait for something outside the host (a
// plugin's gate, a timer, a link, a held commit): the task a test's `delivered()` joins (quiesce). hops is guarded by mu.
func (l *Loop) joined(fn func()) {
	l.mu.Lock()
	l.hopStartLocked()
	l.mu.Unlock()
	l.Go(func() {
		defer l.hopDone()
		fn()
	})
}

func (l *Loop) hopDone() {
	l.mu.Lock()
	l.hopEndLocked()
	l.mu.Unlock()
}

// task runs fn as an owned background task of the loop that is counted until it ends, or waits parked (awaitTask): `await
// work()` of Pi, whose continuation the join waits for unless the work waits for something outside. work is the function fn
// runs, for the report of a stalled join. The count starts here, in the caller's section, so no join sees a gap between the
// call that starts a task and the task running.
func (l *Loop) task(work any, fn func()) { l.start(work, fn, false) }

// pacedTask is task for the work of an ending: the tasks pace waits for (see pacer).
func (l *Loop) pacedTask(work any, fn func()) { l.start(work, fn, true) }

func (l *Loop) start(work any, fn func(), paced bool) {
	t := &parking.Task{Loop: (*taskCounter)(l), Work: work}
	l.mu.Lock()
	l.hopStartLocked()
	if l.inflight == nil {
		l.inflight = map[*parking.Task]struct{}{}
	}
	l.inflight[t] = struct{}{}
	if paced {
		l.pacing.startLocked(t)
	}
	l.mu.Unlock()
	l.Go(func() {
		leave := t.Enter()
		defer func() {
			leave()
			l.mu.Lock()
			delete(l.inflight, t)
			l.hopEndLocked()
			if paced {
				l.pacing.endLocked(t)
			}
			l.mu.Unlock()
		}()
		fn()
	})
}

// counted is the loop whose join counts the tasks started on l: l itself. A loop that wraps another (jobSections) embeds
// sectionLoop and so answers for the loop it wraps; one that only fronts it must say it (a compile error otherwise), so
// a wrapper cannot hide the host's loop from the tasks started on it (goTask would take it for a loop with nothing to join,
// and a test's `delivered()` would run ahead of them: a job's reservation, run, outcome and finish).
func (l *Loop) counted() *Loop { return l }

// goTask is Loop.task for a host's loop; a loop that counts nothing (counted is nil: a test's virtual-time loop) has
// nothing to join.
func goTask(loop sectionLoop, work any, fn func()) {
	if l := loop.counted(); l != nil {
		l.task(work, fn)
		return
	}
	loop.Go(fn)
}

// goPacedTask is Loop.pacedTask for a host's loop; a loop that counts nothing has nothing to join and nobody to pace.
func goPacedTask(loop sectionLoop, work any, fn func()) {
	if l := loop.counted(); l != nil {
		l.pacedTask(work, fn)
		return
	}
	goTask(loop, work, fn)
}

// endingMade counts an ending of loop that is made, pending until its paced task ends (Loop.endingMade).
func endingMade(loop sectionLoop) {
	if l := loop.counted(); l != nil {
		l.endingMade()
	}
}

// startTask is Pi's unawaited async call (`void work()`, a `.then` that nobody awaits): fn runs as a task of the loop that a
// test's `delivered()` waits for until it ends or waits parked (parkedOn), as Pi's chain of microtasks to its next wait for
// something outside the host. The repository's rule is the same: an unawaited promise is an owned task, joined at close.
func startTask(loop sectionLoop, fn func()) { goTask(loop, fn, fn) }

// parkedOn waits for done to close, as a task that waits parked: the join does not wait for it until the wait is over.
func parkedOn(done <-chan struct{}) { _, _ = chordctx.Receive(chordctx.Background(), done) }

// taskCounter is the Loop as the parking package's Counter: the methods are not the Loop's own API.
type taskCounter Loop

// Parked implements parking.Counter: the task waits, so it is not counted.
func (c *taskCounter) Parked(p *parking.Parked) {
	l := (*Loop)(c)
	l.mu.Lock()
	if l.parked == nil {
		l.parked = map[*parking.Parked]struct{}{}
	}
	l.parked[p] = struct{}{}
	l.hopEndLocked()
	l.pacing.parkLocked(p.Task)
	l.mu.Unlock()
}

// Resumed implements parking.Counter: the wait is over, so the task is counted again; once for a wait, whoever says so first.
func (c *taskCounter) Resumed(p *parking.Parked) {
	l := (*Loop)(c)
	l.mu.Lock()
	if _, waiting := l.parked[p]; waiting {
		delete(l.parked, p)
		l.hopStartLocked()
		l.pacing.resumeLocked(p.Task)
	}
	l.mu.Unlock()
}

// Pacing is Pi's microtask checkpoint before the next frame. A frame arrives over a real transport as one macrotask (a
// websocket's message event, an IPC port's: link/websocket.ts:45, ipc.ts:34): Pi runs every microtask the frame's handling
// queued, an ending's chain (`await teardown.previous`, `await attempt.close()`, `await documents.settled()`:
// host/object.ts:1625-1633) to its next wait for something outside, before the next frame is read, so a peer that holds and
// releases faster than a teardown runs finds at most the endings of one frame pending. A Go link's reader goroutine has no
// such checkpoint, so pace is it: the reader calls it once the frame's section ended, and it returns when no paced ending
// can run, each ended or parked on a real wait (a native's close, a held commit step, a drain: chordctx.Receive, Idle); an
// ending parked on a wait that only a later frame of the link ends (a handler awaiting a remote result) does not hold the
// reader: it reads on, the frame arrives, and the endings behind that ending wait for it as reactions, unbounded, as in Pi.
//
// No lock or section is held across the wait: the reader is the goroutine of the link's channel, which calls Message
// unlocked, and a paced task waits for the peer's frames only parked. A reader that is inside a section (a link on the
// loop's own queue, a test's) never waits: it would wait for tasks that need the section.
//
// pacer is the count: tasks are the paced tasks that have not ended, running those of them that are neither parked on a real
// wait nor in a transport's send (sending), pending the endings made and not ended (a chain's endings that wait for their
// predecessor are no task yet). quiet is closed when running falls to zero. All guarded by Loop.mu, the lock of the join's
// count, so the wake-up cannot be lost between a reader's last look at running and its wait.
//
// A task is the identity, never the ID of a goroutine: the goroutine that runs it changes while it runs (a handler's coroutine
// is adopted for it, parking.Adopt, while the task's goroutine waits for the coroutine), so an index of tasks by goroutine ID
// is stale after the first adoption, which kept an ended task and its work reachable and left a send of the coroutine unmarked.
// Pi has no such index either: the identity is the async frame, and nothing of a settled promise stays (host/object.ts:1625,
// host.ts:1690). The pacer keeps the tasks and nothing else of them; the task a goroutine runs is parking's to say (Current).
type pacer struct {
	tasks   map[*parking.Task]struct{}
	running int
	pending int
	// peak is the most endings that were pending at once; waiting the readers that wait in pace now.
	peak    int
	waiting int
	quiet   chan struct{}
	// live is len(tasks), for the send's fast path.
	live atomic.Int32
}

func (p *pacer) runningLocked() {
	if p.running == 0 {
		p.quiet = make(chan struct{})
	}
	p.running++
}

func (p *pacer) quietLocked() {
	p.running--
	if p.running == 0 {
		releaseQuiet(&p.quiet)
	}
}

func (p *pacer) startLocked(t *parking.Task) {
	if p.tasks == nil {
		p.tasks = map[*parking.Task]struct{}{}
	}
	p.tasks[t] = struct{}{}
	p.live.Add(1)
	p.runningLocked()
}

// endLocked: t ended, from running (a task ends when it is not parked).
func (p *pacer) endLocked(t *parking.Task) {
	delete(p.tasks, t)
	p.live.Add(-1)
	p.pending--
	p.quietLocked()
}

func (p *pacer) parkLocked(t *parking.Task) {
	if _, paced := p.tasks[t]; paced {
		p.quietLocked()
	}
}

func (p *pacer) resumeLocked(t *parking.Task) {
	if _, paced := p.tasks[t]; paced {
		p.runningLocked()
	}
}

// endingMade counts an ending that is made: it is pending until its task ends.
func (l *Loop) endingMade() {
	l.mu.Lock()
	l.pacing.pending++
	l.pacing.peak = max(l.pacing.peak, l.pacing.pending)
	l.mu.Unlock()
}

// pace blocks until no paced task is running, or until a stop channel (the link's close, the host's) is closed. It is called
// outside a section, by the goroutine that reads a link, after the section of a frame.
func (l *Loop) pace(linkClosed, hostClosed <-chan struct{}) {
	if l.onLoop() {
		return
	}
	for {
		l.mu.Lock()
		if l.pacing.running == 0 {
			l.mu.Unlock()
			return
		}
		quiet := l.pacing.quiet
		l.pacing.waiting++
		l.mu.Unlock()
		stopped := false
		select {
		case <-quiet:
		case <-linkClosed:
			stopped = true
		case <-hostClosed:
			stopped = true
		}
		l.mu.Lock()
		l.pacing.waiting--
		l.mu.Unlock()
		if stopped {
			return
		}
	}
}

// sending marks the paced task the caller runs, if it is one, as waiting for the transport: a send of a link may block on
// the transport's backpressure (a process's socket), which is a real wait whoever the peer is: two hosts that churn to each
// other, each reader paced on a task blocked sending to a peer that does not read, would wait for each other. It returns the
// task, to be given to sent after the send; nil for a caller that is no paced task.
func (l *Loop) sending() *parking.Task {
	if l.pacing.live.Load() == 0 {
		return nil
	}
	t := parking.Current()
	if t == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, paced := l.pacing.tasks[t]; !paced {
		return nil
	}
	l.pacing.quietLocked()
	return t
}

// sent ends the wait sending began.
func (l *Loop) sent(t *parking.Task) {
	if t == nil {
		return
	}
	l.mu.Lock()
	l.pacing.runningLocked()
	l.mu.Unlock()
}

// pace is Loop.pace for the reader of one of this host's links, whose closing context is closing: a host whose loop is not a
// *Loop (a test's virtual-time loop) has no goroutines to pace.
func (h *chordHost) pace(closing context.Context) {
	if l, ok := h.loop().(*Loop); ok {
		l.pace(closing.Done(), h.routing.Done())
	}
}

// paceSend and paceSent bracket a send of one of this host's links (Loop.sending).
func (h *chordHost) paceSend() *parking.Task {
	if l, ok := h.loop().(*Loop); ok {
		return l.sending()
	}
	return nil
}

func (h *chordHost) paceSent(t *parking.Task) {
	if t != nil {
		h.loop().(*Loop).sent(t)
	}
}

// quiesce blocks until no hop and no task is running: each is ended, or a task waits parked on something not over. A hop or
// a task that ended may have started another before it ended (a section it ran queued one): the count never falls to zero
// in between. When the count is zero, a parked task whose wait is over already (a gate the caller released just before) is
// counted again at once, though its goroutine has not run on yet, and the join goes on waiting for it.
func (l *Loop) quiesce() {
	for {
		l.mu.Lock()
		quiet, busy := l.quiet, l.hops > 0
		var waiting []*parking.Parked
		if !busy {
			for p := range l.parked {
				waiting = append(waiting, p)
			}
		}
		l.mu.Unlock()
		if busy {
			l.waitCount(quiet)
			continue
		}
		over := false
		for _, p := range waiting {
			if p.Ready() {
				(*taskCounter)(l).Resumed(p)
				over = true
			}
		}
		if !over {
			return
		}
	}
}

// waitCount waits until the count of running hops and tasks falls to zero (quiet is closed), and reports the tasks that
// keep it up when that takes joinStall.
func (l *Loop) waitCount(quiet <-chan struct{}) {
	after := joinStall
	if l.stall > 0 {
		after = l.stall
	}
	timer := time.NewTimer(after)
	defer timer.Stop()
	select {
	case <-quiet:
	case <-timer.C:
		panic(l.stalled(after))
	}
}

// stalled is the report of a join that waited joinStall: the tasks that run (not parked), by the function each runs and the
// stack of its goroutine.
func (l *Loop) stalled(after time.Duration) string {
	l.mu.Lock()
	waiting := map[*parking.Task]bool{}
	for p := range l.parked {
		waiting[p.Task] = true
	}
	var running []*parking.Task
	for t := range l.inflight {
		if !waiting[t] {
			running = append(running, t)
		}
	}
	hops := l.hops
	l.mu.Unlock()
	var report strings.Builder
	fmt.Fprintf(&report, "chord: the host loop's join waited %s for %d running (not parked) tasks of %d counted: each blocks on something the loop does not run without parking (a raw channel receive, a lock?); wait with chordctx.Receive or chordctx.Await, or a host primitive:\n", after, len(running), hops)
	stacks := make([]byte, 1<<20)
	dump := string(stacks[:runtime.Stack(stacks, true)])
	for _, t := range running {
		fmt.Fprintf(&report, "\ntask %s\n%s\n", workName(t.Work), stacksOf(dump, t))
	}
	if owner := l.owner.Load(); owner != 0 {
		fmt.Fprintf(&report, "\nthe section that runs now:\n%s\n", stackOf(dump, owner))
	}
	return report.String()
}

// workName is the name and the source line of the function a task runs.
func workName(work any) string {
	value := reflect.ValueOf(work)
	if value.Kind() != reflect.Func {
		return "(unknown)"
	}
	pc := value.Pointer()
	function := runtime.FuncForPC(pc)
	if function == nil {
		return "(unknown)"
	}
	file, line := function.FileLine(pc)
	return fmt.Sprintf("%s (%s:%d)", function.Name(), file, line)
}

// stackOf is the block of goroutine gid in a dump of every goroutine's stack.
func stackOf(dump string, gid uint64) string {
	header := fmt.Sprintf("goroutine %d [", gid)
	for block := range strings.SplitSeq(dump, "\n\n") {
		if strings.HasPrefix(block, header) {
			return block
		}
	}
	return "(no stack)"
}

// stacksOf is the blocks of the goroutines that run t in a dump of every goroutine's stack: the task's own and, while a handler
// it adopted a coroutine for runs, the coroutine's, which is where the task is blocked.
func stacksOf(dump string, t *parking.Task) string {
	ids := t.Goroutines()
	if len(ids) == 0 {
		return "(no stack)"
	}
	blocks := make([]string, len(ids))
	for i, id := range ids {
		blocks[i] = stackOf(dump, id)
	}
	return strings.Join(blocks, "\n\n")
}
