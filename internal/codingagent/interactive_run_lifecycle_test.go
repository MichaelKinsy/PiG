package codingagent

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
)

func newLifecycleMode(t *testing.T) (*InteractiveMode, context.Context, context.CancelFunc) {
	t.Helper()
	m := newPendingDisplayHarness(t)
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.agent = agent.NewAgent(agent.AgentOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	return m, ctx, cancel
}

// A run's UI cleanup is queued to the owner loop after the run settles. When
// the next run starts before that task runs, the stale cleanup must not mark
// the new, still active run idle (DIM-002; reproduced from the Codex review
// with two real runTurn calls and no owner loop, so the old tasks run late).
func TestOldRunCleanupDoesNotMarkTheNextRunIdle(t *testing.T) {
	m, ctx, _ := newLifecycleMode(t)
	m.runTurn(ctx, "", func(context.Context) ([]agent.AgentMessage, error) { return nil, nil })
	oldTasks := []func(){<-m.uiTaskCh, <-m.uiTaskCh}

	secondStarted := make(chan struct{})
	release := make(chan struct{})
	m.runTurn(ctx, "", func(context.Context) ([]agent.AgentMessage, error) {
		close(secondStarted)
		<-release
		return nil, nil
	})
	<-secondStarted
	for _, task := range oldTasks {
		task()
	}
	idle, active := m.isIdle, m.turnActive.Load()
	close(release)
	_ = m.waitForIdle(ctx)
	if idle && active {
		t.Fatal("the previous run's cleanup marked the active run idle")
	}
}

// A session replacement waits for the aborted run to settle, however long a
// cancellation-resistant tool or extension takes, and keeps servicing the
// owner loop meanwhile; it never proceeds while the run is active (DIM-001).
func TestSettleActiveRunWaitsForACancellationResistantRun(t *testing.T) {
	m, ctx, _ := newLifecycleMode(t)
	release := make(chan struct{})
	m.runTurn(ctx, "", func(context.Context) ([]agent.AgentMessage, error) {
		<-release // ignores the abort
		return nil, nil
	})

	settled := make(chan error, 1)
	go func() { settled <- m.settleActiveRun() }()
	select {
	case err := <-settled:
		t.Fatalf("settleActiveRun returned (%v) while the run was active", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-settled:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("settleActiveRun did not return after the run settled")
	}
	if m.turnActive.Load() {
		t.Fatal("settleActiveRun returned before the run settled")
	}
}

// If the mode shuts down first, the replacement is refused rather than made
// under a live run.
func TestSettleActiveRunFailsWhenTheModeShutsDown(t *testing.T) {
	m, ctx, cancel := newLifecycleMode(t)
	release := make(chan struct{})
	defer close(release)
	m.runTurn(ctx, "", func(context.Context) ([]agent.AgentMessage, error) {
		<-release
		return nil, nil
	})
	settled := make(chan error, 1)
	go func() { settled <- m.settleActiveRun() }()
	cancel()
	select {
	case err := <-settled:
		if err == nil {
			t.Fatal("settleActiveRun reported success while the run was still active")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("settleActiveRun ignored shutdown")
	}
}
