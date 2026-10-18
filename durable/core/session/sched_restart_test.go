package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// Pi harness-cancellation-barrier.test.ts "upgrades restart marks down the tree when an abort is requested above them" and
// harness-scheduler-indexes.test.ts: a task abandoned after a restart is abort-marked with reason restart before anything
// of it or below it runs, a task whose definition is missing then waits instead of becoming orphaned, and a request on the
// owner replaces the mark down the tree, which orphans it.
func TestARestartMarksAbandonedWorkAndARequestUpgradesIt(t *testing.T) {
	mem := sessiontest.New()
	first := newSession(mem, mem.NextID())
	hold := &session.TaskDef{Name: "t.hold", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	missing := &session.TaskDef{Name: "t.missing", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	var top, middle, leaf, anchor session.ID
	if _, err := first.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		if top, err = tx.CreateTask(hold, nil, session.TaskOptions{ConversationID: idOf(root)}); err != nil {
			return err
		}
		if middle, err = tx.CreateTask(hold, nil, session.TaskOptions{OwnerTask: top, AbandonOnRestart: true}); err != nil {
			return err
		}
		if leaf, err = tx.CreateTask(missing, nil, session.TaskOptions{OwnerTask: middle}); err != nil {
			return err
		}
		anchor, err = tx.CreateTask(missing, nil, session.TaskOptions{OwnerTask: top})
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	if rec, _ := mem.Task(middle); !rec.Has("abandonOnRestart") {
		t.Fatal("the abandoned task's record does not carry abandonOnRestart")
	}
	var ran []session.ID
	abortCommits := func(rt *session.Runtime, _ *jv.Object, done func(error)) {
		rt.Commit(func(*session.Tx, *jv.Object) (*jv.Object, error) {
			return obj("status", session.StatusTerminal, "outcome", obj("status", "aborted")), nil
		}, done)
	}
	registry := snap{"t.hold": {Def: *hold, Phase: func(rt *session.Runtime, rec *jv.Object, done func(error)) {
		ran = append(ran, idOf(rec))
		// The owner waits for its anchor, so no run invocation outlives its phase.
		rt.Commit(func(*session.Tx, *jv.Object) (*jv.Object, error) {
			return obj("status", session.StatusWaiting, "checkpoint", obj("phase", "wait"), "on", []any{float64(anchor)}, "policy", "allSettled"), nil
		}, done)
	}, Abort: abortCommits}}
	loop := &session.Loop{}
	second := newSession(mem, mem.NextID())
	sched := session.NewScheduler(second, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(error, *jv.Object) {}, Now: func() float64 { return 1 }, SettleOutcome: func(*session.Tx, *jv.Object, *jv.Object) error { return nil }})
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	sched.Resume()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	record := func(id session.ID) *jv.Object {
		rec, err := mem.Task(id)
		if err != nil || rec == nil {
			t.Fatalf("task %d: %v %v", id, rec, err)
		}
		return rec
	}
	// The abandoned middle task is marked restart and ends aborted; its missing-definition leaf carries the mark and waits.
	if got, _ := record(middle).Get("abortReason"); got != "restart" {
		t.Fatalf("the abandoned task's reason is %v, want restart", got)
	}
	if got, _ := record(leaf).Get("abortReason"); got != "restart" {
		t.Fatalf("the leaf's reason is %v, want restart passed on by the cascade", got)
	}
	if taskStatusOf(record(leaf)) == session.StatusTerminal {
		t.Fatal("the leaf ended while its definition was missing")
	}
	for _, id := range ran {
		if id == middle || id == leaf {
			t.Fatalf("phases ran for %v: nothing of an abandoned task or below it runs again", ran)
		}
	}
	// An owner's own cleanup (keepRestart) leaves the mark; a request replaces it, which upgrades the work below.
	abort := func(id session.ID, keepRestart bool) {
		sched.Abort(id, keepRestart, nil, func(_ string, err error) {
			if err != nil {
				t.Error(err)
			}
		})
		if err := loop.Run(); err != nil {
			t.Fatal(err)
		}
	}
	abort(middle, true)
	if got, _ := record(middle).Get("abortReason"); got != "restart" {
		t.Fatalf("an abort that keeps the restart mark left reason %v", got)
	}
	abort(middle, false)
	if record(middle).Has("abortReason") {
		t.Fatal("a request on the abandoned task left its restart reason")
	}
	// A request on the top task reaches the work below it.
	sched.Abort(top, false, nil, func(_ string, err error) {
		if err != nil {
			t.Error(err)
		}
	})
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id   session.ID
		want string
	}{{leaf, "orphaned"}, {middle, "aborted"}, {anchor, "orphaned"}, {top, "aborted"}} {
		state, _ := record(c.id).Get("state")
		outcome, _ := state.(*jv.Object).Get("outcome")
		var got any
		if o, ok := outcome.(*jv.Object); ok {
			got, _ = o.Get("status")
		}
		if got != c.want {
			t.Errorf("task %d is %s with outcome %v, want %s", c.id, taskStatusOf(record(c.id)), got, c.want)
		}
	}
}

// Pi runs the abort handler of a builtin task as an async function that awaits its final commit (generation.ts abort), and
// #runAbort awaits that handler before it enqueues its step (scheduler.ts :1399-1411): the step reaches the line
// AbortReturnHops after the commit job ended, not at once, so a host call made in the same pass (a task phase's
// runtime.commit) enqueues first (Pi E23: job 222 before job 223). A handler that runs in the host keeps zero.
func TestTheAbortStepIsEnqueuedAbortReturnHopsAfterTheHandlerCommit(t *testing.T) {
	for _, tc := range []struct {
		name string
		hops int
		want []int
	}{{"handler of the core", 3, []int{3}}, {"handler in the host", 0, nil}} {
		t.Run(tc.name, func(t *testing.T) {
			mem := sessiontest.New()
			hold := &session.TaskDef{Name: "t.hold", Version: 1, Initial: func(any) any { return obj("phase", "start") }}
			var top session.ID
			first := newSession(mem, mem.NextID())
			if _, err := first.Commit(func(tx *session.Tx) error {
				root, err := tx.CreateRootConversation()
				if err != nil {
					return err
				}
				top, err = tx.CreateTask(hold, nil, session.TaskOptions{ConversationID: idOf(root), AbandonOnRestart: true})
				return err
			}, session.TxScope{}); err != nil {
				t.Fatal(err)
			}
			registry := snap{"t.hold": {Def: *hold, AbortReturnHops: tc.hops, Phase: func(rt *session.Runtime, _ *jv.Object, done func(error)) {
				rt.Commit(func(*session.Tx, *jv.Object) (*jv.Object, error) {
					return obj("status", session.StatusWaiting, "checkpoint", obj("phase", "wait"), "on", []any{float64(top + 99)}, "policy", "allSettled"), nil
				}, done)
			}, Abort: func(rt *session.Runtime, _ *jv.Object, done func(error)) {
				rt.Commit(func(*session.Tx, *jv.Object) (*jv.Object, error) {
					return obj("status", session.StatusTerminal, "outcome", obj("status", "aborted")), nil
				}, done)
			}}}
			loop := &session.Loop{}
			var got []int
			second := newSession(mem, mem.NextID())
			sched := session.NewScheduler(second, loop, session.Env{
				Snapshot: func() session.Snapshot { return registry },
				Report:   func(error, *jv.Object) {},
				Now:      func() float64 { return 1 },
				After: func(n int, then func()) {
					got = append(got, n)
					loop.Defer(then)
				},
			})
			sched.Open(func(err error) {
				if err != nil {
					t.Error(err)
				}
			})
			sched.Resume()
			if err := loop.Run(); err != nil {
				t.Fatal(err)
			}
			rec, err := mem.Task(top)
			if err != nil || rec == nil || taskStatusOf(rec) != session.StatusTerminal {
				t.Fatalf("the abandoned task did not end aborted: %v %v", rec, err)
			}
			var abortHops []int
			for _, n := range got {
				if n == 3 {
					abortHops = append(abortHops, n)
				}
			}
			if len(abortHops) != len(tc.want) {
				t.Fatalf("After was asked for %v, the abort tail wants %v", got, tc.want)
			}
		})
	}
}
