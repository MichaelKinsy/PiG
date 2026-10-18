package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// A conversation's idle wait ignores work below a background task: its owned child task, and a task living in a conversation
// that the background task owns (scheduler.ts #idle with #inScope). Everything here keeps running while the wait settles.
func TestWaitForIdleIgnoresWorkBelowABackgroundTask(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	def := &session.TaskDef{Name: "t.node", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	var root, below, bgID session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		r, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		root = idOf(r)
		bg, err := tx.CreateTask(def, nil, session.TaskOptions{ConversationID: root, Background: true})
		if err != nil {
			return err
		}
		bgID = bg
		return nil
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	running := 0
	later := func() {
		if _, err := s.Commit(func(tx *session.Tx) error {
			if _, err := tx.CreateTask(def, nil, session.TaskOptions{OwnerTask: bgID}); err != nil {
				return err
			}
			c2, err := tx.CreateConversation(session.Ownership{Task: bgID})
			if err != nil {
				return err
			}
			below, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: idOf(c2)})
			return err
		}, session.TxScope{}); err != nil {
			t.Fatal(err)
		}
	}
	registry := snap{"t.node": {Def: *def, Phase: func(*session.Runtime, *jv.Object, func(error)) { running++ }}}
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
	later()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if running != 3 {
		t.Fatalf("running tasks = %d, want the background task, its child and %d", running, below)
	}
	idle := false
	sched.AbortConversation(root, false, nil, func(err error) {
		if err != nil {
			t.Error(err)
		}
		idle = true
	})
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if !idle {
		t.Fatal("the idle wait of the root conversation did not settle with only background work below it")
	}
}
