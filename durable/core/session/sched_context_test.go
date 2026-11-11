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

	sched.KeepContext(root, session.ContextRange{Tail: 5}, false)
	if r, ok := sched.KeptContext(root); !ok || r.Tail != 5 {
		t.Fatalf("a read of an idle conversation keeps its range: %v %v", r.Tail, ok)
	}
	if len(armed) != 1 || armed[0] != 2000 {
		t.Fatalf("one timer for the expiry: %v", armed)
	}
	now = 1500
	sched.KeepContext(root, session.ContextRange{Tail: 7}, false)
	sched.KeepContext(root, session.ContextRange{Tail: 6}, false) // an older tail does not replace the newer range
	if r, _ := sched.KeptContext(root); r.Tail != 7 {
		t.Fatalf("a newer range stays: %v", r.Tail)
	}
	if len(armed) != 1 {
		t.Fatalf("the retention period continues from the first idle read, no new timer: %v", armed)
	}
	if len(released) != 0 {
		t.Fatalf("a kept range releases nothing: %v", released)
	}
	now = 2000
	fire()
	if _, ok := sched.KeptContext(root); ok {
		t.Fatal("the range expires at the first idle check after the retention period")
	}
	if len(released) != 1 || released[0] != root {
		t.Fatalf("the expired range tells the core to release the conversation's context once: %v", released)
	}
	retention = 0
	sched.KeepContext(root, session.ContextRange{Tail: 7}, false)
	if _, ok := sched.KeptContext(root); ok {
		t.Fatal("zero retention keeps nothing")
	}
	if len(released) != 1 {
		t.Fatalf("a range that is not kept releases nothing: the caller still reads the view it derived: %v", released)
	}
	retention = 1000
	sched.KeepContext(root, session.ContextRange{Tail: 7}, true)
	if _, ok := sched.KeptContext(root); ok {
		t.Fatal("a read that finishes after its invocation ended keeps nothing")
	}
	sched.KeepContext(root, session.ContextRange{Tail: 7}, false)
	sched.Seal()
	if _, ok := sched.KeptContext(root); ok || cleared == 0 {
		t.Fatal("seal drops the ranges and clears the timer")
	}
}

// contextFixture is a scheduler over a Session whose root conversation has some entries, and a clock the test moves.
type contextFixture struct {
	t        *testing.T
	s        *session.Session
	sched    *session.Scheduler
	root     session.ID
	now      float64
	retained float64
	released []session.ID
}

func newContextFixture(t *testing.T) *contextFixture {
	t.Helper()
	mem := sessiontest.New()
	f := &contextFixture{t: t, s: newSession(mem, mem.NextID()), now: 1000, retained: 1000}
	if _, err := f.s.Commit(func(tx *session.Tx) error {
		r, err := tx.CreateRootConversation()
		f.root = idOf(r)
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	loop := &session.Loop{}
	f.sched = session.NewScheduler(f.s, loop, session.Env{
		Snapshot: func() session.Snapshot { return snap{} }, Report: func(error, *jv.Object) {},
		Now:                func() float64 { return f.now },
		ContextRetentionMs: func() float64 { return f.retained },
		Timer:              func(float64, func()) func() { return func() {} },
		ReleaseContext:     func(id session.ID) { f.released = append(f.released, id) },
	})
	f.sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *contextFixture) append(kind string, head session.ID) session.ID {
	f.t.Helper()
	return appendKind(f.t, f.s, f.root, kind, head)
}

func (f *contextFixture) kept() session.ContextRange {
	f.t.Helper()
	r, ok := f.sched.KeptContext(f.root)
	if !ok {
		f.t.Fatal("no range is kept")
	}
	return r
}

// harness-context-range.test.ts and scheduler.ts #advanceContexts: a kept range that reached the newest entry when it was read takes the
// entries of later commits as they are published, so a later read scans nothing.
func TestAKeptContextRangeAdvancesWithTheEntriesOfLaterCommits(t *testing.T) {
	f := newContextFixture(t)
	first := f.append("note", 0)
	f.sched.KeepContext(f.root, session.ContextRange{Tail: first, Current: true}, false)
	second := f.append("note", 0)
	third := f.append("note", 0)
	if r := f.kept(); r.Tail != third || !r.Current || r.Head != 0 || second >= third {
		t.Fatalf("range after two commits = %+v, want tail %d, current, no head marker", r, third)
	}
	// A new head marker moves the range's start to the marker's head.
	marker := f.append("pi.compaction", third)
	if r := f.kept(); r.Tail != marker || r.Head != marker || r.From != third || !r.Current {
		t.Fatalf("range after a compaction = %+v, want head %d from %d, tail %d", r, marker, third, marker)
	}
	// Heads only move forward: an older head is stale and the range is dropped, left to the next read.
	f.append("pi.compaction", second)
	if _, ok := f.sched.KeptContext(f.root); ok {
		t.Fatal("a range whose head marker went back stays")
	}
	if len(f.released) != 1 || f.released[0] != f.root {
		t.Fatalf("released = %v, want the dropped range told to the core", f.released)
	}
}

func TestAKeptContextRangeCutOffAtAnEarlierEntryIsNotAdvanced(t *testing.T) {
	f := newContextFixture(t)
	first := f.append("note", 0)
	f.append("note", 0)
	// A range cut off at an earlier entry is not current: the next read extends it from Storage.
	f.sched.KeepContext(f.root, session.ContextRange{Tail: first}, false)
	f.append("note", 0)
	if r := f.kept(); r.Tail != first || r.Current {
		t.Fatalf("range = %+v, want it left at tail %d", r, first)
	}
}

// A commit published while the read ran off the line is not in the range: keeping it checks the Session's known newest entry.
func TestAKeptContextRangeThatMissedACommitIsNotCurrent(t *testing.T) {
	f := newContextFixture(t)
	first := f.append("note", 0)
	f.append("note", 0)
	f.sched.KeepContext(f.root, session.ContextRange{Tail: first, Current: true}, false)
	if r := f.kept(); r.Current {
		t.Fatalf("range = %+v, want it not current: the Session knows a newer entry", r)
	}
	f.append("note", 0)
	if r := f.kept(); r.Tail != first {
		t.Fatalf("range = %+v, want it left at tail %d", r, first)
	}
}

func TestAnIdleKeptContextRangePastItsRetentionIsDroppedByTheNextEntry(t *testing.T) {
	f := newContextFixture(t)
	first := f.append("note", 0)
	f.sched.KeepContext(f.root, session.ContextRange{Tail: first, Current: true}, false)
	f.now += 600
	f.append("note", 0)
	if r := f.kept(); r.Tail != first+1 {
		t.Fatalf("range within its retention = %+v, want it advanced", r)
	}
	// The expiry was fixed when the idle period began: a longer retention now does not extend it.
	f.retained = 10_000
	f.now += 600
	f.append("note", 0)
	if _, ok := f.sched.KeptContext(f.root); ok {
		t.Fatal("a range past its retention stays")
	}
}

func TestAnEntryOfTheKeptRangeIsKnownWithoutAStorageRead(t *testing.T) {
	f := newContextFixture(t)
	f.append("note", 0)
	first := f.append("note", 0)
	f.sched.KeepContext(f.root, session.ContextRange{From: first, Tail: first + 3, Current: true}, false)
	for id, want := range map[session.ID]bool{first - 1: false, first: true, first + 3: true, first + 4: false} {
		if got := f.sched.KeptEntry(f.root, id); got != want {
			t.Errorf("KeptEntry(%d) = %v, want %v", id, got, want)
		}
	}
}
