package session_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

type singleTask struct{ impl *session.TaskImpl }

func (r singleTask) Task(name string) *session.TaskImpl {
	if name == r.impl.Def.Name {
		return r.impl
	}
	return nil
}

func terminal(result float64) *jv.Object {
	outcome := jv.NewObject()
	outcome.Set("status", "completed")
	outcome.Set("result", result)
	state := jv.NewObject()
	state.Set("status", "terminal")
	state.Set("outcome", outcome)
	return state
}

// A phase that drives its commit through BeginHost and FinishHost, as a host does across events, writes what the callback
// staged and the task's own state in one commit, and the task settles terminal.
func TestRuntimeHostCommitWritesTheTasksStateWithItsStagedWrites(t *testing.T) {
	mem := sessiontest.New()
	sess := session.New(mem, mem.NextID(), func() float64 { return 1000 })
	loop := &session.Loop{}
	var phaseErr error
	impl := &session.TaskImpl{Def: session.TaskDef{Name: "host", Version: 1, Initial: func(any) any { return jv.NewObject() }}}
	impl.Phase = func(rt *session.Runtime, task *jv.Object, done func(error)) {
		tx, current, err := rt.BeginHost()
		if err == nil {
			draft := jv.NewObject()
			draft.Set("kind", "note")
			_, err = tx.AppendEntry(rt.ConversationID(), draft)
		}
		if err == nil {
			_, err = rt.FinishHost(tx, current, terminal(7))
		}
		phaseErr = err
		done(err)
	}
	sched := session.NewScheduler(sess, loop, session.Env{Snapshot: func() session.Snapshot { return singleTask{impl} }, Now: func() float64 { return 1000 }})
	var opened error
	sched.Open(func(err error) { opened = err })
	if err := loop.Run(); err != nil || opened != nil {
		t.Fatal(err, opened)
	}
	var taskID session.ID
	if _, err := sess.Commit(func(tx *session.Tx) error {
		c, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		cid, _ := c.Get("id")
		taskID, err = tx.CreateTask(&impl.Def, jv.NewObject(), session.TaskOptions{ConversationID: session.ID(cid.(float64))})
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	sched.Resume()
	if err := loop.Run(); err != nil || phaseErr != nil {
		t.Fatal(err, phaseErr)
	}
	rec, _ := mem.Task(taskID)
	state, _ := rec.Get("state")
	if got := jv.Stringify(state); got != `{"status":"terminal","outcome":{"status":"completed","result":7}}` {
		t.Fatalf("task state %s", got)
	}
	if n := len(mem.Commits); n < 2 {
		t.Fatalf("commits %d", n)
	}
	last := mem.Commits[len(mem.Commits)-1]
	kinds := map[session.WriteKind]int{}
	for _, w := range last.Writes {
		kinds[w.Kind]++
	}
	if kinds[session.WriteEntry] != 1 || kinds[session.WriteTask] != 1 {
		t.Fatalf("the staged entry and the task state land in one commit: %v", kinds)
	}
	for _, w := range last.Writes {
		if w.Kind == session.WriteEntry {
			if by, _ := w.Record.Get("byTaskId"); by != float64(taskID) {
				t.Fatalf("the entry is attributed to the task: byTaskId %v", by)
			}
		}
	}
}

// The checks of Commit run at BeginHost: a task that is not running refuses a host transaction.
func TestRuntimeBeginHostRefusesATaskThatIsNotRunning(t *testing.T) {
	mem := sessiontest.New()
	sess := session.New(mem, mem.NextID(), func() float64 { return 1000 })
	loop := &session.Loop{}
	var refused error
	impl := &session.TaskImpl{Def: session.TaskDef{Name: "host", Version: 1, Initial: func(any) any { return jv.NewObject() }}}
	impl.Phase = func(rt *session.Runtime, task *jv.Object, done func(error)) {
		tx, current, err := rt.BeginHost()
		if err != nil {
			done(err)
			return
		}
		_, err = rt.FinishHost(tx, current, terminal(1))
		if err != nil {
			done(err)
			return
		}
		_, _, refused = rt.BeginHost()
		done(nil)
	}
	sched := session.NewScheduler(sess, loop, session.Env{Snapshot: func() session.Snapshot { return singleTask{impl} }, Now: func() float64 { return 1000 }})
	sched.Open(func(error) {})
	_ = loop.Run()
	if _, err := sess.Commit(func(tx *session.Tx) error {
		c, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		cid, _ := c.Get("id")
		_, err = tx.CreateTask(&impl.Def, jv.NewObject(), session.TaskOptions{ConversationID: session.ID(cid.(float64))})
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	sched.Resume()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if refused == nil || !strings.Contains(refused.Error(), "is terminal") {
		t.Fatalf("a terminal task must refuse a host transaction, got %v", refused)
	}
}

// A read waits on the Session line behind the commits queued before it, and an ended invocation refuses it.
func TestRuntimeReadWaitsBehindQueuedCommitsAndRefusesAnEndedInvocation(t *testing.T) {
	mem := sessiontest.New()
	sess := session.New(mem, mem.NextID(), func() float64 { return 1000 })
	loop := &session.Loop{}
	var order []string
	var kept *session.Runtime
	impl := &session.TaskImpl{Def: session.TaskDef{Name: "host", Version: 1, Initial: func(any) any { return jv.NewObject() }}}
	impl.Phase = func(rt *session.Runtime, task *jv.Object, done func(error)) {
		kept = rt
		rt.Commit(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
			order = append(order, "commit")
			return nil, nil
		}, func(error) {})
		rt.Read(func() error { order = append(order, "read"); return nil }, func(err error) {
			order = append(order, "read done")
			rt.Commit(func(*session.Tx, *jv.Object) (*jv.Object, error) { return terminal(1), nil }, done)
		})
	}
	sched := session.NewScheduler(sess, loop, session.Env{Snapshot: func() session.Snapshot { return singleTask{impl} }, Now: func() float64 { return 1000 }})
	sched.Open(func(error) {})
	_ = loop.Run()
	if _, err := sess.Commit(func(tx *session.Tx) error {
		c, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		cid, _ := c.Get("id")
		_, err = tx.CreateTask(&impl.Def, jv.NewObject(), session.TaskOptions{ConversationID: session.ID(cid.(float64))})
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	sched.Resume()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "commit,read,read done" {
		t.Fatalf("the read runs after the commit queued before it: %s", got)
	}
	var refused error
	kept.Read(func() error { return nil }, func(err error) { refused = err })
	_ = loop.Run()
	if refused == nil {
		t.Fatal("an ended invocation refuses a read")
	}
}
