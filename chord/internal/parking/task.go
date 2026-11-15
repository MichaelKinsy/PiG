package parking

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// A Task is a goroutine a loop started for work that blocks (the host's awaitTask: Pi's `await work()`) and counts while it
// runs. The loop's count is what a test's `delivered()` joins: Pi's `setTimeout(0)` runs after every promise continuation
// whose promise has settled already, and not after one still pending on something outside the host (a test's gate, a timer),
// so a task is counted until it ends or waits for such a thing. The task waits with a host primitive that says so: Recv
// (chordctx.Await, chordctx.Receive) or Idle. A task that blocks any other way stays counted: it is a misuse of the host's
// contract (handlers and tasks park only through host primitives), which the loop's join reports.
type Task struct {
	// Loop counts the task.
	Loop Counter
	// Work is the function the task runs: what a report of a stalled join names.
	Work any
	gid  atomic.Uint64
}

// GID is the ID of the goroutine that runs the task; zero before it started.
func (t *Task) GID() uint64 { return t.gid.Load() }

// Counter is the loop that counts a task.
type Counter interface {
	// Parked is the task starting to wait for something the loop does not run: it is not counted while parked.
	Parked(p *Parked)
	// Resumed is the end of that wait: the task counts again. It is idempotent, and may run on any goroutine: the task's, or
	// the join's that saw the wait over before the task ran on.
	Resumed(p *Parked)
}

// Parked is one wait of a task.
type Parked struct {
	Task *Task
	// Ready reports whether the wait is over, with no effect on the wait: the event it waits for has happened, whether or not
	// the task's goroutine has run on yet. It is what lets a join that finds the task parked count it again at once when the
	// gate it waits for was released just before, without waiting for the scheduler to run the goroutine (which on one
	// processor does not run it before the joining goroutine's next block).
	Ready func() bool
}

// tasks are the running tasks by the ID of their goroutine.
var tasks sync.Map

// Enter registers the calling goroutine as the one that runs t; the returned function ends it.
func (t *Task) Enter() (leave func()) {
	id := goroutineid.Current()
	t.gid.Store(id)
	tasks.Store(id, t)
	return func() { tasks.Delete(id) }
}

// Adopt makes the goroutine gid, a coroutine the calling goroutine switches to and waits for (a handler's), run the calling
// goroutine's task as well, so its waits park the task. The returned function ends the adoption. Outside a task it does nothing.
func Adopt(gid uint64) (end func()) {
	task := current()
	if task == nil {
		return nothing
	}
	task.gid.Store(gid)
	tasks.Store(gid, task)
	return func() { tasks.CompareAndDelete(gid, task) }
}

// current is the task the calling goroutine runs, if any.
func current() *Task {
	found, ok := tasks.Load(goroutineid.Current())
	if !ok {
		return nil
	}
	return found.(*Task)
}

func nothing() {}

// Idle marks the task the calling goroutine runs, if any, as waiting for something the loop does not run until the returned
// function is called (right after the wait). ready reports, with no effect on the wait, whether the wait is over: it must not
// consume what the wait waits for.
func Idle(ready func() bool) (resume func()) {
	task := current()
	if task == nil {
		return nothing
	}
	p := &Parked{Task: task, Ready: ready}
	task.Loop.Parked(p)
	return func() { task.Loop.Resumed(p) }
}

// Recv waits for a value of work or the end of ctx, whose cause it returns. A task waits parked, and answers the loop's
// question whether the wait is over (Parked.Ready) from its own goroutine, since a value a channel hands over cannot be asked
// about without receiving it. The caller has parked the handler's signal already (Park).
func Recv[V any](ctx context.Context, work <-chan V) (V, error) {
	var zero V
	task := current()
	if task == nil {
		select {
		case value := <-work:
			return value, nil
		case <-ctx.Done():
			return zero, context.Cause(ctx)
		}
	}
	// ask is the loop's question: the task looks at the channel on its own goroutine and answers once it did; left is closed at
	// the end of the wait, which answers a question that came too late.
	ask, answer, left := make(chan struct{}), make(chan bool), make(chan struct{})
	p := &Parked{Task: task, Ready: func() bool {
		select {
		case ask <- struct{}{}:
			return <-answer
		case <-left:
			return true
		}
	}}
	task.Loop.Parked(p)
	defer func() {
		task.Loop.Resumed(p)
		close(left)
	}()
	for {
		select {
		case value := <-work:
			return value, nil
		case <-ctx.Done():
			return zero, context.Cause(ctx)
		case <-ask:
			select {
			case value := <-work:
				task.Loop.Resumed(p)
				answer <- true
				return value, nil
			case <-ctx.Done():
				task.Loop.Resumed(p)
				answer <- true
				return zero, context.Cause(ctx)
			default:
				answer <- false
			}
		}
	}
}
