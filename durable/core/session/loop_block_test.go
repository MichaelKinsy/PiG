package session_test

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// A line job waits while something outside the Loop holds the Session line, runs after Kick, and a job queued behind it
// keeps its order.
func TestLoopLineJobsWaitForAnExternalHolder(t *testing.T) {
	held := true
	loop := &session.Loop{Blocked: func() bool { return held }}
	var order []string
	loop.Line(func() error { order = append(order, "a"); return nil }, func(error) { order = append(order, "a done") })
	loop.Line(func() error { order = append(order, "b"); return nil }, nil)
	if err := loop.Run(); err != nil || len(order) != 0 {
		t.Fatalf("a held line runs nothing: %v %v", err, order)
	}
	held = false
	loop.Kick()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(order); got != "[a b a done]" {
		t.Fatalf("order %s", got)
	}
	if !loop.Idle() {
		t.Fatal("the loop is idle after both jobs")
	}
}

// A holder that takes the line between queueing and running a job stops it from running.
func TestLoopBlockedAfterQueueingStopsTheJob(t *testing.T) {
	held := false
	loop := &session.Loop{Blocked: func() bool { return held }}
	ran := false
	loop.Line(func() error { ran = true; return nil }, nil)
	held = true
	if err := loop.Run(); err != nil || ran {
		t.Fatalf("the job ran under a holder: %v %v", err, ran)
	}
	held = false
	loop.Kick()
	if err := loop.Run(); err != nil || !ran {
		t.Fatalf("the job did not run after the release: %v %v", err, ran)
	}
}

// A job whose work has not returned holds the line: a job queued meanwhile (a host call reaching Pi's line during the job) starts when the
// line is released, LineRelease ticks after the work returns, which is before the held job's caller resumes (its outcome is delivered two
// ticks after that), and the job the caller queues on resuming comes behind it.
func TestLoopHeldJobReleasesTheLineBeforeItsCallerResumes(t *testing.T) {
	loop := &session.Loop{Elapsed: func(session.Ended) (int, int) { return 0, 3 }}
	var order []string
	loop.Line(func() error { order = append(order, "reserve"); return nil }, func(error) {
		order = append(order, "reserve done")
		loop.Line(func() error { order = append(order, "step"); return nil }, nil)
	})
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if !loop.Idle() {
		t.Fatal("everything ran")
	}
	loop = &session.Loop{Elapsed: func(session.Ended) (int, int) { return 0, 3 }}
	order = nil
	loop.Line(func() error { order = append(order, "reserve"); return nil }, func(error) {
		order = append(order, "reserve done")
		loop.Line(func() error { order = append(order, "step"); return nil }, nil)
	})
	loop.Defer(func() {
		loop.Line(func() error { order = append(order, "follow-up"); return nil }, nil)
	})
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(order); got != "[reserve follow-up reserve done step]" {
		t.Fatalf("order %s", got)
	}
}

// Admission closes at Seal: a job enqueued after it is rejected without running, one admitted before still runs.
func TestLoopAdmitRejectsAtEnqueue(t *testing.T) {
	closed := false
	errClosed := fmt.Errorf("closed")
	loop := &session.Loop{Admit: func() error {
		if closed {
			return errClosed
		}
		return nil
	}}
	var order []string
	loop.Line(func() error { order = append(order, "before"); return nil }, func(err error) { order = append(order, fmt.Sprint("before done ", err)) })
	closed = true
	loop.Line(func() error { order = append(order, "after"); return nil }, func(err error) { order = append(order, fmt.Sprint("after done ", err)) })
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(order); got != "[before after done closed before done <nil>]" {
		t.Fatalf("order %s", got)
	}
}

// A maintain job is the Session's own upkeep (session.ts #maintain): it asserts nothing, so it runs after admission closed.
func TestLoopMaintainJobRunsAfterAdmissionClosed(t *testing.T) {
	loop := &session.Loop{Admit: func() error { return fmt.Errorf("closed") }}
	ran := false
	loop.LineMaintain(func() error { ran = true; return nil })
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("the maintain job was dropped by the admission check")
	}
}

// A job that touches a key an unpublished commit writes keeps the line while it waits for the commit's publication: a later job and a bare job
// (a host transaction's turn) wait behind it, microtasks outside the line keep running, and the job runs again from the start once the part is
// published, after which the queued jobs run in order.
func TestLoopJobThatWaitsForAPublicationHoldsTheLineAndRunsAgain(t *testing.T) {
	mem := sessiontest.New()
	sess := session.New(mem, mem.NextID(), func() float64 { return 1 })
	loop := bound(sess)
	loop.StoreTicks = func([][]session.Write) (int, int, int) { return 0, 0, 20 }
	var order []string
	sess.Writer().Admit(&session.Part{Writes: []session.Write{{Kind: session.WriteConversation, Record: obj("id", 1.0)}}, Keys: []string{"conv:1"}})
	runs := 0
	loop.Line(func() error {
		runs++
		order = append(order, fmt.Sprint("a", runs))
		return sess.Touch("conv:1")
	}, func(error) { order = append(order, "a done") })
	loop.Line(func() error { order = append(order, "b"); return nil }, nil)
	loop.LineBare(func() error { order = append(order, "tx"); return nil })
	loop.Defer(func() { order = append(order, "micro") })
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(order); got != "[a1 micro a2 b a done tx]" {
		t.Fatalf("order %s", got)
	}
	if !loop.Idle() {
		t.Fatal("the loop is idle after the part was published")
	}
}

// A host job takes its turn on the line like any job, holds it while the host's callback runs, and goes on as a job whose body ran in the core
// once the host ends it: its outcome is delivered after the jobs before it, and a job queued behind it starts LineRelease ticks after the end.
func TestLoopHostJobHoldsTheLineUntilTheHostEndsIt(t *testing.T) {
	loop := &session.Loop{}
	var order []string
	host := loop.LineHost(func() error { order = append(order, "grant"); return nil }, func(err error) { order = append(order, fmt.Sprint("host done ", err)) })
	loop.Line(func() error { order = append(order, "b"); return nil }, nil)
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(order); got != "[grant]" || loop.Idle() {
		t.Fatalf("a host job holds the line until its end: %s", got)
	}
	host.End(nil)
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(order); got != "[grant b host done <nil>]" {
		t.Fatalf("order %s", got)
	}
}

// A host job whose transaction was abandoned while it waited takes nothing and has no outcome: the job behind it runs.
func TestLoopHostJobNotGrantedTakesNothing(t *testing.T) {
	loop := &session.Loop{}
	var order []string
	loop.LineHost(func() error { return session.ErrHostNotGranted }, func(err error) { order = append(order, "never") })
	loop.Line(func() error { order = append(order, "b"); return nil }, nil)
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(order) != "[b]" || !loop.Idle() {
		t.Fatalf("order %v idle %v", order, loop.Idle())
	}
}
