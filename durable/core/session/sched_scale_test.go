package session_test

import (
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// A chain of held tasks costs a number of steps linear in its length (upstream cb437549: a chain of 500 took 12 s, 20,000
// never finished, because every reconcile pass visited every live task and walked up from each to the root). 20,000
// tasks, each owning the next, are created in one commit above a live leaf; they end top-down and hold, then the leaf
// ends and the whole chain finalizes.
func TestAChainOfHeldTasksFinalizesInLinearWork(t *testing.T) {
	chainOfHeldTasksFinalizes(t, 20_000)
}

// Ending one task of a held chain allocates the same at any chain length. The test counts the bytes the end phase
// allocates, which are exact where a clock is not: a pass over the live tasks on every end allocates per task visited, so
// the end of a chain twice as long costs about twice as much and the whole phase is quadratic (Wake once walked every live
// task on each commit).
func TestEndingATaskOfAHeldChainAllocatesTheSameAtAnyLength(t *testing.T) {
	const short, long = 2_000, 4_000
	perEndShort := chainOfHeldTasksFinalizes(t, short) / (short + 1)
	perEndLong := chainOfHeldTasksFinalizes(t, long) / (long + 1)
	if perEndLong > perEndShort+perEndShort/4 {
		t.Fatalf("ending a task of a chain of %d allocates %d bytes, and of a chain of %d allocates %d: the cost of an end grows with the chain", short, perEndShort, long, perEndLong)
	}
}

// Wake names the earliest sleep not yet due whether or not a task is running, and is idle only when nothing runs and
// nothing is due (scheduler.ts wake). It reads the sleeping tasks from their index, so a sleep that ends, wakes or is
// replaced leaves it.
func TestWakeNamesTheEarliestSleepWhileATaskRuns(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	runtimes := map[session.ID]*session.Runtime{}
	dones := map[session.ID]func(error){}
	def := &session.TaskDef{Name: "t.hold", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	var running, late, early session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		for _, id := range []*session.ID{&running, &late, &early} {
			if *id, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: idOf(root)}); err != nil {
				return err
			}
		}
		return nil
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	now := 1.0
	registry := snap{"t.hold": {Def: *def, Phase: func(rt *session.Runtime, rec *jv.Object, done func(error)) {
		runtimes[idOf(rec)], dones[idOf(rec)] = rt, done
	}}}
	loop := &session.Loop{}
	sched := session.NewScheduler(s, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(error, *jv.Object) {}, Now: func() float64 { return now }})
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	sched.Resume()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	// step moves a running task to state and returns from its phase, as a task's code does when it sleeps or ends.
	step := func(id session.ID, state *jv.Object) {
		runtimes[id].Commit(func(*session.Tx, *jv.Object) (*jv.Object, error) { return state, nil }, func(err error) {
			if err != nil {
				t.Error(err)
			}
		})
		if err := loop.Run(); err != nil {
			t.Fatal(err)
		}
		dones[id](nil)
		if err := loop.Run(); err != nil {
			t.Fatal(err)
		}
	}
	// A task that slept is no longer running: the host changes its record, as the harness does when it wakes or ends one.
	set := func(id session.ID, state *jv.Object) {
		if _, err := s.Commit(func(tx *session.Tx) error {
			rec, err := tx.Task(id)
			if err != nil {
				return err
			}
			next := jv.Clone(rec).(*jv.Object)
			next.Set("state", state)
			return tx.SetTask(next)
		}, session.TxScope{}); err != nil {
			t.Fatal(err)
		}
		if err := loop.Run(); err != nil {
			t.Fatal(err)
		}
	}
	sleep := func(until float64) *jv.Object { return obj("status", session.StatusSleeping, "until", until) }
	end := obj("status", session.StatusTerminal, "outcome", obj("status", "completed"))
	expect := func(when string, want session.Wake) {
		t.Helper()
		if got := sched.Wake(); got != want {
			t.Fatalf("%s: wake %+v, want %+v", when, got, want)
		}
	}
	expect("three tasks running", session.Wake{Active: true})
	step(late, sleep(9000))
	expect("one asleep, one running", session.Wake{Active: true, HasAt: true, At: 9000})
	step(early, sleep(5000))
	expect("two asleep, one running", session.Wake{Active: true, HasAt: true, At: 5000})
	set(early, end)
	expect("the earlier sleep ended", session.Wake{Active: true, HasAt: true, At: 9000})
	step(running, end)
	expect("nothing runs, one asleep", session.Wake{HasAt: true, At: 9000})
	now = 9000
	expect("the sleep is due", session.Wake{Active: true})
	now = 1
	set(late, sleep(7000))
	expect("the sleep replaced by an earlier one", session.Wake{HasAt: true, At: 7000})
	set(late, end)
	expect("every task ended", session.Wake{})
}

// chainOfHeldTasksFinalizes creates the chain of depth+1 tasks described above, ends it, and returns the bytes the process
// allocated while it ended.
func chainOfHeldTasksFinalizes(t *testing.T, depth int) uint64 {
	t.Helper()
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	runtimes := map[session.ID]*session.Runtime{}
	def := &session.TaskDef{Name: "t.hold", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	var chain []session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		owner, err := tx.CreateTask(def, nil, session.TaskOptions{ConversationID: idOf(root)})
		if err != nil {
			return err
		}
		chain = append(chain, owner)
		for range depth {
			owner, err = tx.CreateTask(def, nil, session.TaskOptions{OwnerTask: owner})
			if err != nil {
				return err
			}
			chain = append(chain, owner)
		}
		return nil
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	registry := snap{"t.hold": {Def: *def, Phase: func(rt *session.Runtime, rec *jv.Object, _ func(error)) { runtimes[idOf(rec)] = rt }}}
	loop := &session.Loop{}
	sched := session.NewScheduler(s, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(error, *jv.Object) {}, Now: func() float64 { return 1 }})
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	sched.Resume()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != len(chain) {
		t.Fatalf("%d tasks running, want %d", len(runtimes), len(chain))
	}
	end := func(id session.ID) {
		runtimes[id].Commit(func(*session.Tx, *jv.Object) (*jv.Object, error) {
			return obj("status", session.StatusTerminal, "outcome", obj("status", "completed")), nil
		}, func(err error) {
			if err != nil {
				t.Error(err)
			}
		})
		if err := loop.Run(); err != nil {
			t.Fatal(err)
		}
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	// Top-down: each ends holding its outcome while the work below it lives.
	for _, id := range chain[:len(chain)-1] {
		end(id)
	}
	end(chain[len(chain)-1])
	runtime.ReadMemStats(&after)
	for _, id := range []session.ID{chain[0], chain[len(chain)/2], chain[len(chain)-1]} {
		rec, err := mem.Task(id)
		if err != nil {
			t.Fatal(err)
		}
		if taskStatusOf(rec) != session.StatusTerminal {
			t.Fatalf("task %d is %s, want terminal", id, taskStatusOf(rec))
		}
	}
	return after.TotalAlloc - before.TotalAlloc
}

func taskStatusOf(rec *jv.Object) string {
	state, _ := rec.Get("state")
	status, _ := state.(*jv.Object).Get("status")
	text, _ := status.(string)
	return text
}

// The scheduler's memory is bounded by live work (upstream ea448f45): 200 subagent cycles under a live parent, each a tool
// task owning a conversation with one task, leave the ownership indexes as the first cycle did.
func TestEndedSubagentChainsAreForgottenOnceTheirWorkEnds(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	runtimes := map[session.ID]*session.Runtime{}
	def := &session.TaskDef{Name: "t.hold", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	var parent, rootID session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		rootID = idOf(root)
		parent, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: rootID})
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	registry := snap{"t.hold": {Def: *def, Phase: func(rt *session.Runtime, rec *jv.Object, _ func(error)) { runtimes[idOf(rec)] = rt }}}
	loop := &session.Loop{}
	sched := session.NewScheduler(s, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(error, *jv.Object) {}, Now: func() float64 { return 1 }})
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	sched.Resume()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	end := func(id session.ID) {
		runtimes[id].Commit(func(*session.Tx, *jv.Object) (*jv.Object, error) {
			return obj("status", session.StatusTerminal, "outcome", obj("status", "completed")), nil
		}, func(err error) {
			if err != nil {
				t.Error(err)
			}
		})
		if err := loop.Run(); err != nil {
			t.Fatal(err)
		}
	}
	cycle := func() {
		var tool, inner session.ID
		if _, err := s.Commit(func(tx *session.Tx) error {
			var err error
			if tool, err = tx.CreateTask(def, nil, session.TaskOptions{OwnerTask: parent}); err != nil {
				return err
			}
			conversation, err := tx.CreateConversation(session.Ownership{Task: tool})
			if err != nil {
				return err
			}
			inner, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: idOf(conversation)})
			return err
		}, session.TxScope{}); err != nil {
			t.Fatal(err)
		}
		if err := loop.Run(); err != nil {
			t.Fatal(err)
		}
		end(inner)
		end(tool)
	}
	cycle()
	baseline := sched.IndexSizes()
	for range 200 {
		cycle()
	}
	if got := sched.IndexSizes(); got != baseline {
		t.Fatalf("index sizes after 200 more ended subagent cycles %+v, want the baseline %+v", got, baseline)
	}
	if got, want := sched.IndexSizes().Below, sched.BelowRecount(); got != want {
		t.Fatalf("below count %d, listed children %d", got, want)
	}
	end(parent)
}

// An ended task a chain load reads again is listed below its owner (upstream ea448f45 scheduler.ts #settle relists it):
// owner -> child -> a conversation the child owns. The child ends and is forgotten; a task created later in that
// conversation loads the chain again, through the ended child, so the owner that then ends holds its outcome until that
// task ends.
func TestAnOwnerHoldsForWorkInAConversationItsEndedChildOwns(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	runtimes := map[session.ID]*session.Runtime{}
	def := &session.TaskDef{Name: "t.hold", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	var owner, child, conversationID session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		if owner, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: idOf(root)}); err != nil {
			return err
		}
		if child, err = tx.CreateTask(def, nil, session.TaskOptions{OwnerTask: owner}); err != nil {
			return err
		}
		conversation, err := tx.CreateConversation(session.Ownership{Task: child})
		conversationID = idOf(conversation)
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	registry := snap{"t.hold": {Def: *def, Phase: func(rt *session.Runtime, rec *jv.Object, _ func(error)) { runtimes[idOf(rec)] = rt }}}
	loop := &session.Loop{}
	sched := session.NewScheduler(s, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(error, *jv.Object) {}, Now: func() float64 { return 1 }})
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	sched.Resume()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	end := func(id session.ID) {
		runtimes[id].Commit(func(*session.Tx, *jv.Object) (*jv.Object, error) {
			return obj("status", session.StatusTerminal, "outcome", obj("status", "completed")), nil
		}, func(err error) {
			if err != nil {
				t.Error(err)
			}
		})
		if err := loop.Run(); err != nil {
			t.Fatal(err)
		}
	}
	status := func(id session.ID) string {
		rec, err := mem.Task(id)
		if err != nil {
			t.Fatal(err)
		}
		return taskStatusOf(rec)
	}
	end(child)
	if got := status(child); got != session.StatusTerminal {
		t.Fatalf("child is %s, want terminal", got)
	}
	var late session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		var err error
		late, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: conversationID})
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	end(owner)
	if got := status(owner); got != session.StatusCompleting {
		t.Fatalf("owner is %s while the task in its ended child's conversation lives, want completing", got)
	}
	end(late)
	if got := status(owner); got != session.StatusTerminal {
		t.Fatalf("owner is %s once the late task ended, want terminal", got)
	}
}

// Pi ea448f45 #setEdge relists the new conversation and queues it for the sweep: a conversation with nothing below it is
// forgotten at once, so the first task created in it loads its chain again.
func TestANewConversationWithNoWorkBelowItIsSweptAtOnce(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	registry := snap{}
	loop := &session.Loop{}
	sched := session.NewScheduler(s, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(error, *jv.Object) {}, Now: func() float64 { return 1 }})
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	sched.Resume()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(func(tx *session.Tx) error {
		_, err := tx.CreateRootConversation()
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if got := sched.IndexSizes(); got.Edges != 0 || got.Roots != 0 {
		t.Fatalf("index sizes after a conversation with no work was created %+v, want its edge swept", got)
	}
}

// gateOnTerminal makes the commit that writes the terminal record of one task wait for the host's Storage once: the line job
// returns a *NeedGate and runs again from its start after the answer (handler.gateCommit).
type gateOnTerminal struct {
	*sessiontest.MemStore
	task  session.ID
	asked bool
}

func (g *gateOnTerminal) Commit(writes []session.Write, nextID session.ID) (session.Seq, error) {
	for _, w := range writes {
		if w.Kind == session.WriteTask && !g.asked && idOf(w.Record) == g.task && taskStatusOf(w.Record) == session.StatusTerminal {
			g.asked = true
			return 0, &session.NeedGate{}
		}
	}
	return g.MemStore.Commit(writes, nextID)
}

// The finalize pass takes the held owners to check once, as Pi's callback does: a commit that waits for the host's Storage
// runs the job again from its start, and the second run still finalizes the chain the first one found.
func TestAGatedFinalizeCommitFinalizesTheChainWhenItRunsAgain(t *testing.T) {
	mem := sessiontest.New()
	gated := &gateOnTerminal{MemStore: mem}
	s := newSession(gated, mem.NextID())
	runtimes := map[session.ID]*session.Runtime{}
	def := &session.TaskDef{Name: "t.hold", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	var top, middle, leaf session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		if top, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: idOf(root)}); err != nil {
			return err
		}
		if middle, err = tx.CreateTask(def, nil, session.TaskOptions{OwnerTask: top}); err != nil {
			return err
		}
		leaf, err = tx.CreateTask(def, nil, session.TaskOptions{OwnerTask: middle})
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	gated.task = middle
	registry := snap{"t.hold": {Def: *def, Phase: func(rt *session.Runtime, rec *jv.Object, _ func(error)) { runtimes[idOf(rec)] = rt }}}
	loop := &session.Loop{}
	sched := session.NewScheduler(s, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(error, *jv.Object) {}, Now: func() float64 { return 1 }})
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	sched.Resume()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []session.ID{top, middle, leaf} {
		runtimes[id].Commit(func(*session.Tx, *jv.Object) (*jv.Object, error) {
			return obj("status", session.StatusTerminal, "outcome", obj("status", "completed")), nil
		}, func(err error) {
			if err != nil {
				t.Error(err)
			}
		})
		if err := loop.Run(); err != nil {
			t.Fatal(err)
		}
		if id == leaf {
			break
		}
	}
	if !gated.asked || !loop.Busy() {
		t.Fatalf("the finalize commit did not wait for the Storage (asked %v, busy %v)", gated.asked, loop.Busy())
	}
	loop.Ungate()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []session.ID{top, middle, leaf} {
		rec, err := mem.Task(id)
		if err != nil {
			t.Fatal(err)
		}
		if got := taskStatusOf(rec); got != session.StatusTerminal {
			t.Fatalf("task %d is %s, want terminal", id, got)
		}
	}
}
