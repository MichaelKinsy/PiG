package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// A kept context range outlives its idle conversation for the retention period, with one timer for the earliest expiry
// (scheduler.ts #contexts, #settleIdle, #scheduleExpiry): a read of an idle conversation starts the period, a later read
// keeps it, a shorter or zero retention drops the range, and a range with a smaller tail never replaces a newer one.
func TestKeptContextRetention(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	var root session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		r, err := tx.CreateRootConversation()
		root = idOf(r)
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	now, retention := 1000.0, 1000.0
	var armed []float64
	var fire func()
	cleared := 0
	var released []session.ID
	loop := &session.Loop{}
	sched := session.NewScheduler(s, loop, session.Env{
		Snapshot: func() session.Snapshot { return snap{} }, Report: func(error, *jv.Object) {},
		Now:                func() float64 { return now },
		ContextRetentionMs: func() float64 { return retention },
		Timer:              func(at float64, f func()) func() { armed, fire = append(armed, at), f; return func() { cleared++ } },
		ReleaseContext:     func(id session.ID) { released = append(released, id) },
	})
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}

	sched.KeepContext(root, 0, 5, false)
	if _, tail, ok := sched.KeptContext(root); !ok || tail != 5 {
		t.Fatalf("a read of an idle conversation keeps its range: %v %v", tail, ok)
	}
	if len(armed) != 1 || armed[0] != 2000 {
		t.Fatalf("one timer for the expiry: %v", armed)
	}
	now = 1500
	sched.KeepContext(root, 0, 7, false)
	sched.KeepContext(root, 0, 6, false) // an older tail does not replace the newer range
	if _, tail, _ := sched.KeptContext(root); tail != 7 {
		t.Fatalf("a newer range stays: %v", tail)
	}
	if len(armed) != 1 {
		t.Fatalf("the retention period continues from the first idle read, no new timer: %v", armed)
	}
	if len(released) != 0 {
		t.Fatalf("a kept range releases nothing: %v", released)
	}
	now = 2000
	fire()
	if _, _, ok := sched.KeptContext(root); ok {
		t.Fatal("the range expires at the first idle check after the retention period")
	}
	if len(released) != 1 || released[0] != root {
		t.Fatalf("the expired range tells the core to release the conversation's context once: %v", released)
	}
	retention = 0
	sched.KeepContext(root, 0, 7, false)
	if _, _, ok := sched.KeptContext(root); ok {
		t.Fatal("zero retention keeps nothing")
	}
	if len(released) != 1 {
		t.Fatalf("a range that is not kept releases nothing: the caller still reads the view it derived: %v", released)
	}
	retention = 1000
	sched.KeepContext(root, 0, 7, true)
	if _, _, ok := sched.KeptContext(root); ok {
		t.Fatal("a read that finishes after its invocation ended keeps nothing")
	}
	sched.KeepContext(root, 0, 7, false)
	sched.Seal()
	if _, _, ok := sched.KeptContext(root); ok || cleared == 0 {
		t.Fatal("seal drops the ranges and clears the timer")
	}
}
