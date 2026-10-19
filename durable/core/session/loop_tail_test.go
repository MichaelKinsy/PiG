package session_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// tickLoop drives a Loop on a virtual microtask clock: Hold keeps each job for its duration in ticks, and Tail and Begin
// hold the line for one tick, as the core wires them to hops chains (sched.go startScheduler).
type tickLoop struct {
	loop *session.Loop
	now  int
	due  []tickDue
	next int // the duration of the job that finishes next; job sets it
}

type tickDue struct {
	at int
	fn func()
}

func newTickLoop() *tickLoop {
	c := &tickLoop{loop: &session.Loop{}}
	hold := func(n int, fn func()) bool {
		if n <= 0 {
			return false
		}
		c.due = append(c.due, tickDue{c.now + n, fn})
		return true
	}
	c.loop.Hold = func(release func()) bool { return hold(c.next+c.loop.TakeStartDelay(), release) }
	c.loop.Tail = func(release func()) bool { return hold(1, release) }
	c.loop.Begin = func(start func()) bool { return hold(1, start) }
	return c
}

func (c *tickLoop) settle(t *testing.T) {
	t.Helper()
	for {
		if err := c.loop.Run(); err != nil {
			t.Fatal(err)
		}
		if len(c.due) == 0 {
			return
		}
		i := slices.IndexFunc(c.due, func(d tickDue) bool {
			return d.at == slices.MinFunc(c.due, func(a, b tickDue) int { return a.at - b.at }).at
		})
		d := c.due[i]
		c.due = slices.Delete(c.due, i, i+1)
		c.now = d.at
		d.fn()
	}
}

// job returns a job body that records its start tick under name and makes the job last dur ticks.
func (c *tickLoop) job(starts map[string]int, name string, dur int, then func()) func() error {
	return func() error {
		starts[name] = c.now + c.loop.StartDelay // an idle line starts the job a tick later: the body runs now and its hold lasts that tick
		c.next = dur
		if then != nil {
			then()
		}
		return nil
	}
}

// A job queued behind a Session.readOnLine job starts one tick after the read job ends: session.ts #enqueue :530-537 starts
// it through `this.#tail.then(job)`, and the tail resolves with the read's run (dcore-trace: Pi's job+ is its predecessor's
// job- + 1 for read and commit jobs alike).
func TestLoopJobQueuedBehindAReadJobStartsATickAfterIt(t *testing.T) {
	c := newTickLoop()
	starts := map[string]int{}
	c.loop.Line(c.job(starts, "a", 3, nil), nil)
	c.loop.ReadLine(c.job(starts, "read", 25, nil), nil)
	c.loop.Line(c.job(starts, "b", 7, nil), nil)
	c.settle(t)
	if got := fmt.Sprint(starts["read"], starts["b"]); got != "4 30" {
		t.Fatalf("read starts at %d, b at %d; want 4 and 30 (one tick after each predecessor ends)", starts["read"], starts["b"])
	}
}

// A readOnLine job queued on an idle line starts a tick after the call, as `this.#tail.then(job)` on a resolved tail does.
func TestLoopReadJobOnAnIdleLineStartsATickLater(t *testing.T) {
	c := newTickLoop()
	starts := map[string]int{}
	c.loop.ReadLine(c.job(starts, "read", 25, nil), nil)
	c.settle(t)
	if starts["read"] != 1 {
		t.Fatalf("read starts at %d, want 1", starts["read"])
	}
}

// A job that the finishing job's publication or continuation queues (the scheduler's reserve pass after a commit) starts one
// tick after the finishing job ends: Pi queues it before the job's run resolves (the commit is published when #runCommit
// resumes from Storage.commit), so it waits for the tail like any queued job.
func TestLoopJobQueuedAsAJobCompletesStartsATickAfterIt(t *testing.T) {
	c := newTickLoop()
	starts := map[string]int{}
	c.loop.Line(c.job(starts, "commit", 29, nil), func(error) {
		c.loop.Line(c.job(starts, "reserve", 7, nil), nil)
	})
	c.loop.Kick()
	c.settle(t)
	if got := fmt.Sprint(starts["commit"], starts["reserve"]); got != "0 30" {
		t.Fatalf("commit starts at %d, reserve at %d; want 0 and 30", starts["commit"], starts["reserve"])
	}
}

// A host caller of a readOnLine job (ClientReadLine) is answered a hop before the job's run resolves, and the line is released
// where it was before (the tail holds the hop): Pi's caller awaits the job's own promise, the tail reacts a hop later
// (session.ts :105-121, :530-537). On an idle line the job starts a tick after the call.
func TestLoopClientReadJobAnswersAHopBeforeItsEndAndKeepsTheLine(t *testing.T) {
	for _, idle := range []bool{true, false} {
		c := newTickLoop()
		c.loop.Hold = func(release func()) bool {
			n := c.next + c.loop.TakeStartDelay()
			if c.loop.EndingClient() && n >= 2 {
				c.due = append(c.due, tickDue{c.now + n - 1, func() { c.loop.Settle(); release() }})
				return true
			}
			c.due = append(c.due, tickDue{c.now + n, release})
			return true
		}
		c.loop.Tail = func(release func()) bool {
			n := 1
			if c.loop.Answered {
				n = 2
			}
			c.due = append(c.due, tickDue{c.now + n, release})
			return true
		}
		starts := map[string]int{}
		answered := -1
		if !idle {
			c.loop.Line(c.job(starts, "a", 3, nil), nil)
		}
		c.loop.ClientReadLine(c.job(starts, "read", 13, nil), func(error) { answered = c.now })
		c.loop.Line(c.job(starts, "b", 7, nil), nil)
		c.settle(t)
		startRead, wantAnswer, wantB := 1, 13, 15
		if !idle {
			startRead, wantAnswer, wantB = 4, 16, 18
		}
		if starts["read"] != startRead || answered != wantAnswer || starts["b"] != wantB {
			t.Fatalf("idle=%v: read starts %d answers %d, b starts %d; want %d, %d, %d (answer at start+n-1, b at start+n+1)",
				idle, starts["read"], answered, starts["b"], startRead, wantAnswer, wantB)
		}
	}
}

// A NextTick asked as a line job finishes (scheduler.ts #run resuming after #step) resumes one tick later, the tick the line's
// tail resolves, and rides the tail's hops chain: no chain of its own, and the line's next job still starts first.
func TestLoopNextTickAfterAJobRidesTheTailChain(t *testing.T) {
	c := newTickLoop()
	begins := 0
	c.loop.Begin = func(start func()) bool { begins++; c.due = append(c.due, tickDue{c.now + 1, start}); return true }
	var order []string
	starts := map[string]int{}
	at := -1
	c.loop.Line(c.job(starts, "step", 5, nil), func(error) {
		c.loop.NextTick(func() { at = c.now; order = append(order, "phase") })
	})
	c.loop.Line(c.job(starts, "next", 1, func() { order = append(order, "next") }), nil)
	c.settle(t)
	// The job ends at tick 5, the tail resolves at 6, and the phase resumes there.
	if begins != 0 || at != 6 || fmt.Sprint(order) != "[next phase]" {
		t.Fatalf("begins %d, phase at %d, order %v; want no chain of its own, tick 6, next before phase", begins, at, order)
	}
}

// A job with nothing to do at its end (no done, no publication) and its tail need no step between them: Hold parks the line for the
// job's last tick and the tail's together (QuietEnd, QuietRelease), and the next job starts where it does with a step of each.
func TestLoopQuietEndStartsTheNextJobWhereTheTailDoes(t *testing.T) {
	run := func(quiet bool) (map[string]int, int) {
		c := newTickLoop()
		steps := 0
		count := func(fn func()) func() { return func() { steps++; fn() } }
		c.loop.Hold = func(release func()) bool {
			n := c.next + c.loop.TakeStartDelay()
			if quiet && c.loop.QuietEnd() {
				c.due = append(c.due, tickDue{c.now + n + 1, count(c.loop.QuietRelease())})
				return true
			}
			c.due = append(c.due, tickDue{c.now + n, count(release)})
			return true
		}
		c.loop.Tail = func(release func()) bool { c.due = append(c.due, tickDue{c.now + 1, count(release)}); return true }
		starts := map[string]int{}
		done := false
		c.loop.Line(c.job(starts, "a", 3, nil), nil)
		c.loop.ReadLine(c.job(starts, "read", 5, nil), nil)
		c.loop.Line(c.job(starts, "b", 4, nil), func(error) { done = true })
		c.loop.Line(c.job(starts, "c", 2, nil), nil)
		c.settle(t)
		if !done {
			t.Fatal("the job with a done did not complete")
		}
		return starts, steps
	}
	want, wantSteps := run(false)
	got, gotSteps := run(true)
	for name, at := range want {
		if got[name] != at {
			t.Errorf("job %s starts at tick %d with the quiet end, %d with a step for the end and one for the tail", name, got[name], at)
		}
	}
	if gotSteps >= wantSteps {
		t.Errorf("%d steps with the quiet end, %d without: the jobs without a done take one step less each", gotSteps, wantSteps)
	}
}
