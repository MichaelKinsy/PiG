package coding

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

// upstream: packages/coding-agent/src/core/agent-session.ts:1379-1400 (dispose) aborts the retry, compaction, branch summary and
// bash work, invalidates the extension runner, drops every listener and disconnects from the agent.
func TestSessionDisposeAbortsWorkAndDropsListeners(t *testing.T) {
	sess := newBashTestSession(t, `{}`)
	events := 0
	sess.Subscribe(func(agent.AgentEvent) { events++ })
	sess.emitOrderedEventSync(agent.AgentEndEvent{})
	if events != 1 {
		t.Fatalf("listener saw %d event(s) before Dispose, want 1", events)
	}

	done := make(chan BashResult, 1)
	go func() {
		result, _ := sess.ExecuteBash(context.Background(), "sleep 30", nil, nil)
		done <- result
	}()
	// ExecuteBash registers its cancel before it spawns the shell, so once it is registered Dispose must cancel the run.
	for deadline := time.Now().Add(10 * time.Second); !bashIsRunning(sess); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("ExecuteBash did not start")
		}
	}

	sess.Dispose()

	select {
	case result := <-done:
		if !result.Cancelled {
			t.Fatalf("running bash command was not cancelled by Dispose: %+v", result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Dispose did not abort the running bash command")
	}
	if runner := sess.ExtensionRunner(); runner != nil && !runner.IsStale() {
		t.Error("Dispose left the extension runner usable; upstream invalidates it")
	}
	before := events
	sess.emitOrderedEventSync(agent.AgentEndEvent{})
	if events != before {
		t.Errorf("listener received %d event(s) after Dispose, want none", events-before)
	}
}
