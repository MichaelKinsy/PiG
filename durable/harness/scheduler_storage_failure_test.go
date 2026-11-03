package harness

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
)

type schedulerReadKey struct{}

// schedulerReadFailure fails a conversation read the scheduler makes with its own context once armed. The scheduler's
// context carries schedulerReadKey because OpenHarness derives it from the context it was given.
type schedulerReadFailure struct {
	*failingCalls
}

func (s schedulerReadFailure) Task(ctx context.Context, id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
	if ctx.Value(schedulerReadKey{}) != nil && s.fail("scheduler-task") {
		return nil, s.err
	}
	return s.failingCalls.Task(ctx, id)
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
	// The Session caches what it writes or reads, so the chain read that fails is of a task no read of this Session ever
	// loaded: live task 11, owned by the ended task 10, seeded before the Harness opens. Opening schedules the reconcile that
	// loads the chain of every live task, which reads task 10 through the line's Storage.
	kind := waitingTask(log).AnyDefinition().Name
	checkpoint := durable.JsonValue(map[string]any{"phase": "work"})
	ended := durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{Id: 10, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: kind, Version: 1,
		State: durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted}}}
	owner := ended.Id
	live := durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{Id: 11, ConversationId: durable.ROOT_CONVERSATION_ID, Owner: &owner, Kind: kind, Version: 1,
		State: durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending, Checkpoint: &checkpoint}}
	if _, err := durabletest.CommitWrites(testContext, store.failingCalls, []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}},
		durable.TaskWrite{Value: ended},
		durable.TaskWrite{Value: live},
	}); err != nil {
		t.Fatal(err)
	}
	store.add("scheduler-task")
	harness, err := OpenHarness(context.WithValue(testContext, schedulerReadKey{}, true), store, HarnessOptions{
		Models:   ai.CreateModels(),
		Registry: registry,
		OnReport: reports.add,
	})
	if err != nil {
		t.Fatal(err)
	}
	sfEnded(t, harness, durable.SessionEndFailed, store.err)
	sfReports(t, reports, store.err)
	if err := harness.Close(testContext); err != nil && !errors.Is(err, store.err) {
		t.Fatal(err)
	}
}
