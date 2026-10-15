package session_test

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/session"
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
	if got := fmt.Sprint(order); got != "[a a done b]" {
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

// dcore-trace A1: a held job's completion waits for its release. A job queued while it is held (a host call reaching
// Pi's line during the job) runs before the held job's continuation, which queues its own job behind it.
func TestLoopHeldJobCompletesAfterReleaseAndKeepsLineOrder(t *testing.T) {
	var release func()
	loop := &session.Loop{Hold: func(r func()) bool {
		if release != nil {
			return false
		}
		release = r
		return true
	}}
	var order []string
	loop.Line(func() error { order = append(order, "reserve"); return nil }, func(error) {
		order = append(order, "reserve done")
		loop.Line(func() error { order = append(order, "step"); return nil }, nil)
	})
	if err := loop.Run(); err != nil || release == nil {
		t.Fatalf("the job was not held: %v %v", err, order)
	}
	if loop.Idle() {
		t.Fatal("a held job keeps the loop busy")
	}
	loop.Line(func() error { order = append(order, "follow-up"); return nil }, nil)
	if err := loop.Run(); err != nil || fmt.Sprint(order) != "[reserve]" {
		t.Fatalf("a job queued during the hold ran early: %v %v", err, order)
	}
	release()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(order); got != "[reserve reserve done follow-up step]" {
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

// A job that returns a NeedGate keeps the line while the host's Storage works: a later job and a bare job (a host
// transaction's turn) wait behind it, microtasks outside the line keep running, and Ungate runs the job again from the
// start, after which the queued jobs run in order.
func TestLoopGatedJobHoldsTheLineAndRunsAgain(t *testing.T) {
	loop := &session.Loop{}
	var order []string
	runs := 0
	loop.Line(func() error {
		runs++
		order = append(order, fmt.Sprint("a", runs))
		if runs == 1 {
			return &session.NeedGate{}
		}
		return nil
	}, func(error) { order = append(order, "a done") })
	loop.Line(func() error { order = append(order, "b"); return nil }, nil)
	loop.LineBare(func() error { order = append(order, "tx"); return nil })
	loop.Defer(func() { order = append(order, "micro") })
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(order); got != "[a1 micro]" || !loop.Busy() {
		t.Fatalf("a gated line runs only what is off the line: %s", got)
	}
	loop.Ungate()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(order); got != "[a1 micro a2 a done b tx]" {
		t.Fatalf("order %s", got)
	}
	if loop.Busy() {
		t.Fatal("the loop is idle after the gate was answered")
	}
}
