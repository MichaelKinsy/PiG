package host

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// A task is one identity however many goroutines run it. In Pi it is an async frame, or the object that holds it, and no thread
// id ever stands for it (host/object.ts:1625-1633: an ending is a promise chain; host.ts:1690-1696: the chain's entry is
// deleted when it emptied). A Go section hands a task from goroutine to goroutine: the section a task's goroutine ran lends
// itself to a handler's coroutine (Loop.Lend), and once the section is over the same goroutine runs what is left of the
// handlers that returned or parked in it, each on its coroutine's goroutine, adopted for the task (Loop.finish, parking.Adopt).
// The cases below put every lookup of the loop that takes a task for the goroutine it finds itself on through that hand-over.

// handlerSettlingAfterItsSection is a section that ends with a handler that returned inside it, and whose settlement runs
// after the section on the goroutine that ended it, on the handler's coroutine: what a call admitted by a section settles with
// (the send of its result).
func handlerSettlingAfterItsSection(loop *Loop, settle func()) {
	loop.Run(func() {
		signal := parking.New()
		loop.Lend(signal, func(bool) {
			signal.Return()
			settle()
		})
	})
}

// A send that may block on the transport is a real wait, which pace does not wait for (Loop.sending): whichever goroutine of
// the paced task makes it. Pi's microtask checkpoint before the next frame ends at a wait for something outside, and a send to
// a transport is one.
func TestPaceMarksTheSendOfAPacedTaskWhicheverGoroutineRunsIt(t *testing.T) {
	for _, from := range []string{"the task's own goroutine", "the coroutine of a handler that finished in the task's section"} {
		t.Run(from, func(t *testing.T) {
			loop := NewLoop()
			inSend, release := make(chan struct{}), make(chan struct{})
			var open sync.Once
			letGo := func() { open.Do(func() { close(release) }) }
			send := func() {
				sending := loop.sending()
				close(inSend)
				<-release
				loop.sent(sending)
			}
			work := func() {
				if from == "the task's own goroutine" {
					send()
					return
				}
				handlerSettlingAfterItsSection(loop, send)
			}
			defer func() { letGo(); loop.quiesce() }()
			endingMade(loop)
			loop.pacedTask(work, work)
			returnsWithin(t, "the paced task's send", func() { <-inSend })
			if running, _, _, _ := pacing(loop); running != 0 {
				t.Fatalf("the paced task is blocked in a send from %s and the pacer counts %d paced tasks running, want 0: pace would wait for a send only the peer's reader can end", from, running)
			}
			returnsWithin(t, "pace while the only paced task is in a send", func() { loop.pace(nil, nil) })
			letGo()
			loop.quiesce()
			if running, pending, _, _ := pacing(loop); running != 0 || pending != 0 {
				t.Fatalf("after the send: paced tasks running %d, endings pending %d, want 0 and 0", running, pending)
			}
		})
	}
}

// A send of a goroutine that is no paced task marks nothing, however the goroutine got to run: the test's own, a task that is not
// paced (an `await work()`: Loop.task), and the coroutine a handler finished on, adopted for that task. The pacer's count is the
// paced tasks', and a send that took one off it for a task that is not on it would let pace return while a paced task runs.
func TestSendingMarksNothingForAGoroutineThatIsNoPacedTask(t *testing.T) {
	loop := NewLoop()
	hold := make(chan struct{})
	pacedRunning(loop, hold, func() {})
	defer func() { close(hold); loop.quiesce() }()
	if sending := loop.sending(); sending != nil {
		t.Fatal("a goroutine that runs no task was marked as a paced task in a send")
	}
	marked, done := make(chan *parking.Task, 2), make(chan struct{})
	work := func() {
		defer close(done)
		marked <- loop.sending()
		handlerSettlingAfterItsSection(loop, func() { marked <- loop.sending() })
	}
	loop.task(work, work)
	returnsWithin(t, "the sends of a task that is not paced", func() { <-done })
	for range 2 {
		if sending := <-marked; sending != nil {
			t.Fatal("a send of a task that is not paced was marked as a paced task's")
		}
	}
	if running, _, _, _ := pacing(loop); running != 1 {
		t.Fatalf("paced tasks running after the sends of a task that is not paced = %d, want the 1 that was never in a send", running)
	}
}

// What a stalled join reports of a running task is the stack of every goroutine that runs it now. A task is not one goroutine:
// while a handler it adopted a coroutine for runs (parking.Adopt), its own goroutine waits for the coroutine, and both are in
// the report; once the handler finished it is the task's own alone. The report of a task blocked on a raw channel after a
// handler finished in its section named the coroutine's goroutine, idle by then (or gone, or running another call's handler)
// and not the task's, so the stack that says what the task waits for was not in it.
func TestStalledNamesOnlyTheTasksOwnGoroutineAfterAHandlerItAdoptedFinished(t *testing.T) {
	loop := NewLoop()
	ids, block := make(chan [2]uint64, 1), make(chan struct{})
	defer func() { close(block); loop.quiesce() }()
	work := func() {
		own := goroutineid.Current()
		var coroutine atomic.Uint64
		handlerSettlingAfterItsSection(loop, func() { coroutine.Store(goroutineid.Current()) })
		ids <- [2]uint64{own, coroutine.Load()}
		<-block // a wait the loop does not run, without saying so: the stall the report is for
	}
	loop.task(work, work)
	var own, coroutine uint64
	returnsWithin(t, "the task's start", func() { got := <-ids; own, coroutine = got[0], got[1] })
	if own == coroutine || coroutine == 0 {
		t.Fatalf("the task's goroutine is %d and the handler's coroutine %d: the case needs two goroutines", own, coroutine)
	}
	report := loop.stalled(time.Second)
	if !strings.Contains(report, fmt.Sprintf("goroutine %d [", own)) {
		t.Fatalf("the report of a task blocked on goroutine %d, after a handler finished in its section, has no stack of it:\n%s", own, report)
	}
	if strings.Contains(report, fmt.Sprintf("goroutine %d [", coroutine)) {
		t.Fatalf("the report of a task blocked on goroutine %d names goroutine %d, the coroutine of a handler that finished:\n%s", own, coroutine, report)
	}
}

// While a handler runs for the task (the task's goroutine waits for its coroutine), the report names both: where the task is
// blocked is the coroutine, and the goroutine that waits for it is the task's own.
func TestStalledNamesTheTasksOwnGoroutineAndTheCoroutineWhileAHandlerItAdoptedRuns(t *testing.T) {
	loop := NewLoop()
	ids, block := make(chan [2]uint64, 1), make(chan struct{})
	defer func() { close(block); loop.quiesce() }()
	work := func() {
		own := goroutineid.Current()
		handlerSettlingAfterItsSection(loop, func() {
			ids <- [2]uint64{own, goroutineid.Current()}
			<-block
		})
	}
	loop.task(work, work)
	var own, coroutine uint64
	returnsWithin(t, "the handler's start", func() { got := <-ids; own, coroutine = got[0], got[1] })
	report := loop.stalled(time.Second)
	for _, id := range []uint64{own, coroutine} {
		if !strings.Contains(report, fmt.Sprintf("goroutine %d [", id)) {
			t.Fatalf("the report of a task on goroutine %d whose handler is blocked on goroutine %d has no stack of goroutine %d:\n%s", own, coroutine, id, report)
		}
	}
}
