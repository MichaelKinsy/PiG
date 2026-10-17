package harness

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

type schedulerReadKey struct{}

// schedulerReadFailure fails a conversation read the scheduler makes with its own context once armed. The scheduler's
// context carries schedulerReadKey because OpenHarness derives it from the context it was given.
type schedulerReadFailure struct {
	*failingCalls
}

func (s schedulerReadFailure) Conversation(ctx context.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	if ctx.Value(schedulerReadKey{}) != nil && s.fail("conversation") {
		return nil, s.err
	}
	return s.failingCalls.Conversation(ctx, id)
}

// Pi scheduler.ts #loadChain reads Storage inside the reconcile commit, through the Session's guard, which fails the
// Session for a read error and runs its close listeners at once, the scheduler's #seal among them (session.ts fail,
// close, #notifyClose). Go's scheduler holds its mutex across the chain load, so the seal must not need it again on that
// goroutine: the Session ends failed with the read's error.
func TestASchedulerChainReadThatFailsTheSessionEndsIt(t *testing.T) {
	store := schedulerReadFailure{newFailingCalls()}
	log := &signalLog{}
	registry := CreateRegistry()
	mustInstall(t, registry, new(durable.Extension{Name: "tasks", Tasks: []durable.AnyTask{waitingTask(log).Erased()}}))
	reports := &reportLog{}
	harness, err := OpenHarness(context.WithValue(testContext, schedulerReadKey{}, true), store, HarnessOptions{
		Models:   ai.CreateModels(),
		Registry: registry,
		OnReport: reports.add,
	})
	if err != nil {
		t.Fatal(err)
	}
	root := mustRoot(t, harness, nil)
	store.add("conversation")
	// The task is unloaded until the reconcile its commit schedules loads its chain: the root conversation's edge.
	if _, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, waitingTask(log), struct{}{}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
	}); err != nil {
		t.Fatal(err)
	}
	sfEnded(t, harness, durable.SessionEndFailed, store.err)
	sfReports(t, reports, store.err)
	if err := harness.Close(testContext); err != nil && !errors.Is(err, store.err) {
		t.Fatal(err)
	}
}
