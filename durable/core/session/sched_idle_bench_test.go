package session_test

// Pi main 35dfa757: the cost of a commit that changes a task must not grow with the conversations keeping an idle context range.

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// idleFixture is a scheduler over a Session whose root has no running task, and conversations that keep an idle context range.
type idleFixture struct {
	t         testing.TB
	s         *session.Session
	loop      *session.Loop
	sched     *session.Scheduler
	def       *session.TaskDef
	root      session.ID
	now       float64
	retention float64
	released  []session.ID
	fire      func()
	armed     []float64
}

func newIdleFixture(t testing.TB) *idleFixture {
	t.Helper()
	f := &idleFixture{t: t, now: 1000, retention: 1000}
	mem := sessiontest.New()
	f.s = newSession(mem, mem.NextID())
	f.def = &session.TaskDef{Name: "t.idle", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	if _, err := f.s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		f.root = idOf(root)
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	f.loop = &session.Loop{}
	f.sched = session.NewScheduler(f.s, f.loop, session.Env{
		Snapshot: func() session.Snapshot { return snap{} }, Report: func(error, *jv.Object) {},
		Now:                func() float64 { return f.now },
		ContextRetentionMs: func() float64 { return f.retention },
		Timer:              func(at float64, fire func()) func() { f.armed, f.fire = append(f.armed, at), fire; return func() {} },
		ReleaseContext:     func(id session.ID) { f.released = append(f.released, id) },
	})
	f.sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	f.run()
	return f
}

func (f *idleFixture) run() {
	f.t.Helper()
	if err := f.loop.Run(); err != nil {
		f.t.Fatal(err)
	}
}

// conversation is a new ownerless conversation.
func (f *idleFixture) conversation() session.ID {
	f.t.Helper()
	var id session.ID
	if _, err := f.s.Commit(func(tx *session.Tx) error {
		c, err := tx.CreateConversation(session.Ownership{})
		id = idOf(c)
		return err
	}, session.TxScope{}); err != nil {
		f.t.Fatal(err)
	}
	f.run()
	return id
}

// task creates a task in the root conversation.
func (f *idleFixture) task() session.ID {
	f.t.Helper()
	var id session.ID
	if _, err := f.s.Commit(func(tx *session.Tx) error {
		var err error
		id, err = tx.CreateTask(f.def, nil, session.TaskOptions{ConversationID: f.root})
		return err
	}, session.TxScope{}); err != nil {
		f.t.Fatal(err)
	}
	f.run()
	return id
}

// end ends a task the way the host does.
func (f *idleFixture) end(id session.ID) {
	f.t.Helper()
	if _, err := f.s.Commit(func(tx *session.Tx) error {
		rec, err := tx.Task(id)
		if err != nil {
			return err
		}
		next := jv.Clone(rec).(*jv.Object)
		next.Set("state", obj("status", session.StatusTerminal, "outcome", obj("status", "completed")))
		return tx.SetTask(next)
	}, session.TxScope{}); err != nil {
		f.t.Fatal(err)
	}
	f.run()
}

// BenchmarkTaskCycleWithKeptContexts is one task created and ended (two commits that change a task) with `contexts` conversations
// keeping an idle range: ns/op and allocs/op must not grow with them.
func BenchmarkTaskCycleWithKeptContexts(b *testing.B) {
	for _, contexts := range []int{100, 1_000, 4_000} {
		b.Run(itoa(contexts), func(b *testing.B) {
			f := newIdleFixture(b)
			for range contexts {
				f.sched.KeepContext(f.conversation(), 0, 5, false)
			}
			b.ReportAllocs()
			for b.Loop() {
				f.end(f.task())
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "contexts=0"
	}
	digits := ""
	for ; n > 0; n /= 10 {
		digits = string(rune('0'+n%10)) + digits
	}
	return "contexts=" + digits
}
