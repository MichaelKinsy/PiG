package session_test

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// switchStore fails every commit while failing is set.
type switchStore struct {
	*sessiontest.MemStore
	failing bool
	// failingReads fails the read of a conversation, which a pass reads to load a task's chain.
	failingReads bool
	err          error
}

func (s *switchStore) Conversation(id session.ID) (*jv.Object, error) {
	if s.failingReads {
		return nil, s.err
	}
	return s.MemStore.Conversation(id)
}

func (s *switchStore) Commit(writes []session.Write, nextID session.ID) (session.Seq, error) {
	if s.failing {
		return 0, s.err
	}
	return s.MemStore.Commit(writes, nextID)
}

// Pi eba84973 scheduler.ts #failSession: a failed commit of the scheduler's own, here the reservation, fails the Session,
// which reports the error once; nothing runs, a later commit is refused with SessionFailed whose cause is the first error,
// and a failed pass is not retried by the next commit (no finalizeTaken, no cascade retry).
func TestAFailedReservationCommitFailsTheSession(t *testing.T) {
	mem := sessiontest.New()
	store := &switchStore{MemStore: mem, err: errors.New("disk gone")}
	s := newSession(store, mem.NextID())
	def := &session.TaskDef{Name: "t.once", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	if _, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		_, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: idOf(root)})
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	ran := 0
	registry := snap{"t.once": {Def: *def, Phase: func(*session.Runtime, *jv.Object, func(error)) { ran++ }}}
	var reports []error
	loop := &session.Loop{}
	sched := session.NewScheduler(s, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(err error, _ *jv.Object) { reports = append(reports, err) }, Now: func() float64 { return 1 }})
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	store.failing = true
	sched.Resume()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || !errors.Is(reports[0], store.err) {
		t.Fatalf("reports %v, want the storage error once", reports)
	}
	if !errors.Is(s.Failure(), store.err) {
		t.Fatalf("failure %v, want the storage error", s.Failure())
	}
	store.failing = false
	_, err := s.Commit(func(tx *session.Tx) error { _, err := tx.CreateRootConversation(); return err }, session.TxScope{})
	var failed *session.FailedError
	if !errors.Is(err, session.ErrSessionFailed) || !errors.As(err, &failed) || !errors.Is(failed.Cause, store.err) {
		t.Fatalf("a commit on the failed Session returned %v, want SessionFailed caused by the storage error", err)
	}
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if ran != 0 {
		t.Fatalf("%d phases ran, want none", ran)
	}
}

// A throw in the scheduler's own commit that is not the store's commit, here a failed read while loading a chain, fails the
// Session too (scheduler.ts #failSession).
func TestAFailedChainLoadFailsTheSession(t *testing.T) {
	mem := sessiontest.New()
	store := &switchStore{MemStore: mem, err: errors.New("disk gone")}
	s := newSession(store, mem.NextID())
	def := &session.TaskDef{Name: "t.once", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	if _, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		_, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: idOf(root)})
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	registry := snap{"t.once": {Def: *def, Phase: func(*session.Runtime, *jv.Object, func(error)) {}}}
	var reports []error
	loop := &session.Loop{}
	sched := session.NewScheduler(s, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(err error, _ *jv.Object) { reports = append(reports, err) }, Now: func() float64 { return 1 }})
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	store.failingReads = true
	sched.Resume()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || !errors.Is(reports[0], store.err) || !errors.Is(s.Failure(), store.err) {
		t.Fatalf("reports %v, failure %v, want the read error once", reports, s.Failure())
	}
}

// Pi eba84973 session.ts fail and scheduler.ts #seal, closedError: a Session that fails seals at once, so its close listener
// rejects every wait the scheduler holds with SessionFailed caused by the storage error, and a wait asked for afterwards
// rejects the same way (closedError gives SessionFailed, not "Harness is closed", once the Session failed).
func TestAFailedSessionRejectsTheSchedulersWaitsWithSessionFailed(t *testing.T) {
	mem := sessiontest.New()
	store := &switchStore{MemStore: mem, err: errors.New("disk gone")}
	s := newSession(store, mem.NextID())
	def := &session.TaskDef{Name: "t.once", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	var task, conv session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		conv = idOf(root)
		task, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: conv})
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	registry := snap{"t.once": {Def: *def, Phase: func(*session.Runtime, *jv.Object, func(error)) {}}}
	loop := &session.Loop{}
	sched := session.NewScheduler(s, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(error, *jv.Object) {}, Now: func() float64 { return 1 }})
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	var taskErr, idleErr error
	taskDone, idleDone := false, false
	sched.WaitForTask(task, nil, func(_ *jv.Object, err error) { taskErr, taskDone = err, true })
	sched.WaitForIdle(conv, nil, func(err error) { idleErr, idleDone = err, true })
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if taskDone || idleDone {
		t.Fatalf("a wait settled before the failure: task %v idle %v", taskErr, idleErr)
	}
	store.failing = true
	sched.Resume()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	failedBy := func(what string, done bool, err error) {
		t.Helper()
		var failed *session.FailedError
		if !done || !errors.As(err, &failed) || !errors.Is(failed.Cause, store.err) {
			t.Fatalf("%s: settled %v with %v, want SessionFailed caused by the storage error", what, done, err)
		}
	}
	failedBy("the task wait held at the failure", taskDone, taskErr)
	failedBy("the idle wait held at the failure", idleDone, idleErr)
	idleDone, idleErr = false, nil
	sched.WaitForIdle(conv, nil, func(err error) { idleErr, idleDone = err, true })
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	failedBy("an idle wait asked after the failure", idleDone, idleErr)
}

// Pi scheduler.ts abort: after the abort commit, `await awaitWithContext(Promise.race([run.done, session.failed]), context)`: a run
// that ignores its signal can outlive a failed Session, which ends the wait with SessionFailed instead of leaving it for ever.
func TestAbortOfARunThatIgnoresItsSignalEndsWithSessionFailedWhenTheSessionFails(t *testing.T) {
	mem := sessiontest.New()
	store := &switchStore{MemStore: mem, err: errors.New("disk gone")}
	s := newSession(store, mem.NextID())
	def := &session.TaskDef{Name: "t.once", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	var task session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		task, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: idOf(root)})
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	started := false
	registry := snap{"t.once": {Def: *def, Phase: func(*session.Runtime, *jv.Object, func(error)) { started = true }}}
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
	if !started {
		t.Fatal("the run never started")
	}
	var result string
	var abortErr error
	done := false
	sched.Abort(task, false, nil, func(r string, err error) { result, abortErr, done = r, err, true })
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatalf("the abort settled (%q, %v) while the run it signalled was still going", result, abortErr)
	}
	store.failing = true
	if _, err := s.Commit(func(tx *session.Tx) error { _, err := tx.CreateRootConversation(); return err }, session.TxScope{}); !errors.Is(err, store.err) {
		t.Fatalf("the commit returned %v, want the storage error", err)
	}
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	var failed *session.FailedError
	if !done || !errors.As(abortErr, &failed) || !errors.Is(failed.Cause, store.err) {
		t.Fatalf("abort settled %v with (%q, %v), want SessionFailed caused by the storage error", done, result, abortErr)
	}
}

// Pi session.ts close and harness.ts beforeClose: close() queues `Promise.resolve().then(beforeClose)` and then runs the close
// listeners; the Harness's seal listener rejects the waits it holds, and join() signals every invocation in that microtask, ahead of
// the waits' reactions and not inside the listener. A listener that runs in the seal sees the invocation's signal still clear.
func TestSealSignalsInvocationsInAMicrotaskAfterTheCloseListenersAheadOfWaitReactions(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	def := &session.TaskDef{Name: "t.once", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	var task session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		task, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: idOf(root)})
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	var rt *session.Runtime
	registry := snap{"t.once": {Def: *def, Phase: func(r *session.Runtime, _ *jv.Object, _ func(error)) { rt = r }}}
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
	if rt == nil {
		t.Fatal("the run never started")
	}
	var order []string
	sched.WaitForTask(task, nil, func(_ *jv.Object, err error) { order = append(order, "wait") })
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	rt.Signal().OnAbort(func() { order = append(order, "signal") })
	var inListener bool
	s.SubscribeClose(func() { inListener = rt.Signal().Aborted() })
	s.Seal()
	if rt.Signal().Aborted() || inListener {
		t.Fatalf("the invocation's signal was aborted inside the seal (listener saw %v), want a microtask later", inListener)
	}
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if !rt.Signal().Aborted() || len(order) != 2 || order[0] != "signal" {
		t.Fatalf("order %v, aborted %v, want the signal first, then the wait's rejection", order, rt.Signal().Aborted())
	}
}
