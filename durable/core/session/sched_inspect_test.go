package session_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

type snap map[string]*session.TaskImpl

func (s snap) Task(name string) *session.TaskImpl { return s[name] }

// Harness.inspect derives each live task's state without running task code and reports the scheduling state
// (scheduler.ts inspect): paused until resume, a newer definition with migrate is ready+migrates, one without it is
// blocked migration_failed, an older one task_too_old, an unknown one missing_task, a task waiting on a live task waits.
func TestInspectDerivesStatesWithoutRunningTasks(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	def := func(name string, version int) *session.TaskDef {
		return &session.TaskDef{Name: name, Version: version, Initial: func(any) any { return jv.NewObject() }}
	}
	ids := map[string]session.ID{}
	if _, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		cid := idOf(root)
		for _, v := range []struct {
			name    string
			version int
		}{{"t.ready", 1}, {"t.migrating", 1}, {"t.nomigrate", 1}, {"t.old", 3}, {"t.missing", 1}} {
			id, err := tx.CreateTask(def(v.name, v.version), nil, session.TaskOptions{ConversationID: cid})
			if err != nil {
				return err
			}
			ids[v.name] = id
		}
		return nil
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	ran := false
	run := func(*session.Runtime, *jv.Object, func(error)) { ran = true }
	registry := snap{
		"t.ready":     {Def: session.TaskDef{Name: "t.ready", Version: 1}, Phase: run},
		"t.migrating": {Def: session.TaskDef{Name: "t.migrating", Version: 2}, Phase: run, Migrate: func(i, c any, _ int) (any, any, error) { return i, c, nil }},
		"t.nomigrate": {Def: session.TaskDef{Name: "t.nomigrate", Version: 2}, Phase: run},
		"t.old":       {Def: session.TaskDef{Name: "t.old", Version: 2}, Phase: run},
	}
	loop := &session.Loop{}
	sched := session.NewScheduler(s, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(error, *jv.Object) {}, Now: func() float64 { return 1 }})
	opened := false
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
		opened = true
	})
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if !opened {
		t.Fatal("the scheduler did not open")
	}
	scheduling, tasks, err := sched.Inspect(registry)
	if err != nil {
		t.Fatal(err)
	}
	if scheduling != "paused" || ran {
		t.Fatalf("scheduling %q, task code ran: %v", scheduling, ran)
	}
	got := map[session.ID]string{}
	for _, task := range tasks {
		got[idOf(task.Record)] = fmt.Sprint(task.Kind, " ", task.Migrates, " ", task.Reason, " ", task.Err)
	}
	want := map[string]string{
		"t.ready": "ready false  <nil>", "t.migrating": "ready true  <nil>",
		"t.nomigrate": "blocked false migration_failed Task t.nomigrate version 2 has no migration from 1",
		"t.old":       "blocked false task_too_old <nil>", "t.missing": "blocked false missing_task <nil>",
	}
	for name, id := range ids {
		if got[id] != want[name] {
			t.Errorf("%s: %q, want %q", name, got[id], want[name])
		}
	}
}

func idOf(o *jv.Object) session.ID {
	v, _ := o.Get("id")
	f, _ := v.(float64)
	return session.ID(f)
}

// A host-run migration (TaskImpl.MigrateAsync): the reservation that needs it waits without running anything, asks
// once, and runs the task migrated when the host answers; a failed answer blocks the task as migration_failed.
func TestHostMigrationAnswersLater(t *testing.T) {
	for _, fail := range []bool{false, true} {
		mem := sessiontest.New()
		s := newSession(mem, mem.NextID())
		var id session.ID
		if _, err := s.Commit(func(tx *session.Tx) error {
			root, err := tx.CreateRootConversation()
			if err != nil {
				return err
			}
			id, err = tx.CreateTask(&session.TaskDef{Name: "t.m", Version: 1, Initial: func(any) any { return jv.NewObject() }}, "old", session.TaskOptions{ConversationID: idOf(root)})
			return err
		}, session.TxScope{}); err != nil {
			t.Fatal(err)
		}
		var ranWith []string
		var answer func(any, any, error)
		asked := 0
		impl := &session.TaskImpl{Def: session.TaskDef{Name: "t.m", Version: 2},
			MigrateAsync: func(input, cp any, from int, done func(any, any, error)) { asked++; answer = done },
			Phase: func(_ *session.Runtime, task *jv.Object, done func(error)) {
				v, _ := task.Get("input")
				ranWith = append(ranWith, fmt.Sprint(v))
				done(nil)
			}}
		registry := snap{"t.m": impl}
		reports := 0
		loop := &session.Loop{}
		sched := session.NewScheduler(s, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(error, *jv.Object) { reports++ }, Now: func() float64 { return 1 },
			// The phase returns without progress, so the scheduler faults the task: nothing else to settle here.
			SettleOutcome: func(*session.Tx, *jv.Object, *jv.Object) error { return nil }})
		sched.Open(func(error) {})
		sched.Resume()
		if err := loop.Run(); err != nil {
			t.Fatal(err)
		}
		if asked != 1 || len(ranWith) != 0 {
			t.Fatalf("fail=%v: asked %d, ran %v before the answer", fail, asked, ranWith)
		}
		sched.RegistryChanged()
		if err := loop.Run(); err != nil || asked != 1 {
			t.Fatalf("fail=%v: a second pass asks again (asked %d, %v)", fail, asked, err)
		}
		if fail {
			answer(nil, nil, errors.New("cannot migrate"))
		} else {
			answer("new", jv.NewObject(), nil)
		}
		if err := loop.Run(); err != nil {
			t.Fatal(err)
		}
		_, tasks, _ := sched.Inspect(registry)
		switch {
		case fail && (reports != 1 || len(ranWith) != 0 || len(tasks) != 1 || tasks[0].Reason != "migration_failed"):
			t.Fatalf("failed answer: reports %d, ran %v, tasks %+v", reports, ranWith, tasks)
		case !fail && (len(ranWith) != 1 || ranWith[0] != "new"):
			t.Fatalf("answer: ran %v", ranWith)
		}
		_ = id
	}
}
