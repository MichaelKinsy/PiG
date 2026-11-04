package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// harness-observers.test.ts "advance a graph observer with a blocked refresh, outside any commit": a registry change tells the graph
// that blocked reasons may have changed, at once and before the pass it starts (scheduler.ts open: the registry subscription calls
// #notifyBlocked, then #kick).
func TestRegistryChangeNotifiesBlockedListenersBeforeThePass(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	registry := snap{}
	notified := 0
	loop := &session.Loop{}
	sched := session.NewScheduler(s, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(error, *jv.Object) {}, Now: func() float64 { return 1 },
		BlockedChanged: func() { notified++ }})
	sched.Open(func(error) {})
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	before := notified
	sched.RegistryChanged()
	if notified != before+1 {
		t.Fatalf("a registry change notified %d times, want 1 before the loop runs", notified-before)
	}
}
