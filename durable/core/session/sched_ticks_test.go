package session_test

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// readCountingStore charges perRead ticks for every conversation and task read, as the handler's Storage reads charge the
// running line job.
type readCountingStore struct {
	*sessiontest.MemStore
	ticks *int
}

const perRead = 4

func (r readCountingStore) Conversation(id session.ID) (*jv.Object, error) {
	*r.ticks += perRead
	return r.MemStore.Conversation(id)
}

func (r readCountingStore) Task(id session.ID) (*jv.Object, error) {
	*r.ticks += perRead
	return r.MemStore.Task(id)
}

// Pi ea448f45 #loadScopes (scheduler.ts:680-693) awaits one #loadChain call per task still unloaded when its turn comes,
// and each await resumes a tick after the call settles, whether or not it read Storage. After a reopen, two live tasks in
// the root conversation are both unloaded: the first call reads the conversation's owner edge, the second finds it known
// and reads nothing. The cascade reconcile that Open schedules also awaits `scanAll` of the queued submissions (util.ts:46-55).
// It therefore takes its two base awaits (#loadScopes, #finalize), the read, a tick for each of the two #loadChain calls,
// and one for scanAll.
func TestTheReconcileChargesATickForEachChainLoadItAwaits(t *testing.T) {
	mem := sessiontest.New()
	def := &session.TaskDef{Name: "t.hold", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	first := newSession(mem, mem.NextID())
	if _, err := first.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		for range 2 {
			if _, err := tx.CreateTask(def, nil, session.TaskOptions{ConversationID: idOf(root)}); err != nil {
				return err
			}
		}
		return nil
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}

	reads := 0
	s := newSession(readCountingStore{MemStore: mem, ticks: &reads}, mem.NextID())
	loop := &session.Loop{}
	var jobs []int
	loop.Hold = func(release func()) bool {
		jobs = append(jobs, s.TakeTicks())
		loop.Defer(release)
		return true
	}
	registry := snap{}
	sched := session.NewScheduler(s, loop, session.Env{
		Snapshot:  func() session.Snapshot { return registry },
		Report:    func(error, *jv.Object) {},
		Now:       func() float64 { return 1 },
		ReadTicks: func() int { return reads },
	})
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	sched.Resume()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	// Two base awaits, one conversation read, the two #loadChain calls and scanAll.
	want := 2 + perRead + 2 + 1
	if slices.Contains(jobs, want) {
		return
	}
	t.Fatalf("transaction ticks of the jobs after reopen %v, want a reconcile of %d (2 awaits, a %d-tick read, 2 #loadChain calls, scanAll)", jobs, want, perRead)
}
