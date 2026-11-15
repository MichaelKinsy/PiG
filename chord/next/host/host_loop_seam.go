package host

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"time"

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
		close(l.quiet)
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
func (l *Loop) task(work any, fn func()) {
	t := &parking.Task{Loop: (*taskCounter)(l), Work: work}
	l.mu.Lock()
	l.hopStartLocked()
	if l.inflight == nil {
		l.inflight = map[*parking.Task]struct{}{}
	}
	l.inflight[t] = struct{}{}
	l.mu.Unlock()
	l.Go(func() {
		leave := t.Enter()
		defer func() {
			leave()
			l.mu.Lock()
			delete(l.inflight, t)
			l.hopEndLocked()
			l.mu.Unlock()
		}()
		fn()
	})
}

// goTask is Loop.task for a host's loop; a loop that is not a *Loop (a test's virtual-time loop) has nothing to join.
func goTask(loop sectionLoop, work any, fn func()) {
	if l, ok := loop.(*Loop); ok {
		l.task(work, fn)
		return
	}
	loop.Go(fn)
}

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
	l.mu.Unlock()
}

// Resumed implements parking.Counter: the wait is over, so the task is counted again; once for a wait, whoever says so first.
func (c *taskCounter) Resumed(p *parking.Parked) {
	l := (*Loop)(c)
	l.mu.Lock()
	if _, waiting := l.parked[p]; waiting {
		delete(l.parked, p)
		l.hopStartLocked()
	}
	l.mu.Unlock()
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
		fmt.Fprintf(&report, "\ntask %s\n%s\n", workName(t.Work), stackOf(dump, t.GID()))
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
