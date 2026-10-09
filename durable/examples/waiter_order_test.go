package examples_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

type gatedState struct {
	Phase string `json:"phase"`
}

// Upstream resolves submission, task, and idle waiters with a promise inside a commit listener, so code awaiting
// submission.wait(), waitForTask() or waitForIdle() resumes only after every listener of that commit ran
// (session.ts #publish, scheduler.ts #observe, submissions.ts). A host subscriber registered after the Harness is the
// last listener; these tests hold it inside the settling publication and require the wait to stay pending until it
// returns. Returning early is the bug; the window only gives such a waiter time to show itself.

const earlyReturnWindow = 50 * time.Millisecond

// holdingSubscriber blocks the first publication that matches until release is closed.
type holdingSubscriber struct {
	once    sync.Once
	seen    chan struct{}
	release chan struct{}
}

func holdOn(t *testing.T, opened harness.Harness, matches func(durable.CommitPublication) bool) *holdingSubscriber {
	t.Helper()
	held := &holdingSubscriber{seen: make(chan struct{}), release: make(chan struct{})}
	unsubscribe := opened.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
		if !matches(publication) {
			return
		}
		held.once.Do(func() {
			close(held.seen)
			<-held.release
		})
	})
	t.Cleanup(unsubscribe)
	return held
}

// expectWaitAfterSubscriber requires done to stay pending while the subscriber is held, then to finish once released.
func expectWaitAfterSubscriber(t *testing.T, held *holdingSubscriber, done <-chan error) {
	t.Helper()
	select {
	case <-held.seen:
	case err := <-done:
		t.Fatalf("the wait returned (%v) before the settling publication reached the last subscriber", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the settling publication never reached the subscriber")
	}
	select {
	case err := <-done:
		close(held.release)
		t.Fatalf("the wait returned (%v) while a subscriber of the settling commit was still running", err)
	case <-time.After(earlyReturnWindow):
	}
	close(held.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the wait did not return after the subscriber did")
	}
}

func terminalTask(publication durable.CommitPublication) bool {
	for _, change := range publication.Changes {
		if write, ok := change.(durable.TaskWrite); ok && write.Value.State.Status == durable.TaskTerminal {
			return true
		}
	}
	return false
}

func TestSubmissionWaitReturnsAfterEveryCommitSubscriber(t *testing.T) {
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: fauxModels(fauxAnswer("Done.")), Registry: harness.CreateRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession(t, opened)
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	held := holdOn(t, opened, func(publication durable.CommitPublication) bool {
		for _, change := range publication.Changes {
			if write, ok := change.(durable.SubmissionWrite); ok && write.Value.Status == durable.SubmissionDone {
				return true
			}
		}
		return false
	})
	submission := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go")}))
	done := make(chan error, 1)
	go func() {
		_, err := submission.Wait(background)
		done <- err
	}()
	expectWaitAfterSubscriber(t, held, done)
}

func TestWaitForTaskAndIdleReturnAfterEveryCommitSubscriber(t *testing.T) {
	for _, wait := range []struct {
		name string
		run  func(harness.Harness, harness.Conversation, durable.TaskId) error
	}{
		{"waitForTask", func(opened harness.Harness, _ harness.Conversation, id durable.TaskId) error {
			_, err := opened.WaitForTask(background, id)
			return err
		}},
		{"conversation waitForIdle", func(_ harness.Harness, root harness.Conversation, _ durable.TaskId) error {
			return root.WaitForIdle(background)
		}},
	} {
		t.Run(wait.name, func(t *testing.T) {
			gate := make(chan struct{})
			type empty = struct{}
			type gatedRuntime = durable.TaskRuntime[empty, gatedState, empty, any]
			gated := durable.DefineTask(durable.TaskDefinition[empty, gatedState, empty, any]{
				Name:    "example.gated",
				Version: 1,
				Initial: func(empty) gatedState { return gatedState{Phase: "run"} },
				Phases: map[string]durable.PhaseHandler[empty, gatedState, empty, any]{
					"run": func(ctx context.Context, _ durable.RunningTask[empty, gatedState, empty], runtime gatedRuntime) error {
						<-gate
						return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[empty, gatedState, empty]) (*durable.NextTaskState[gatedState, empty], error) {
							return &durable.NextTaskState[gatedState, empty]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[empty]{Status: durable.OutcomeCompleted, Result: &empty{}}}, nil
						})
					},
				},
				Abort: func(ctx context.Context, _ durable.RunningTask[empty, gatedState, empty], runtime gatedRuntime) error {
					return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[empty, gatedState, empty]) (*durable.NextTaskState[gatedState, empty], error) {
						return &durable.NextTaskState[gatedState, empty]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[empty]{Status: durable.OutcomeAborted}}, nil
					})
				},
			})
			registry := harness.CreateRegistry()
			installed(t, registry, new(durable.Extension{Name: "gated", Tasks: []durable.AnyTask{gated}}))
			opened := openHarness(t, registry, nil)
			defer closeSession(t, opened)
			root := must(opened.Root(background, nil))
			id := commit(t, root, func(tx durable.Tx) (durable.TaskId, error) {
				return durable.CreateTask(tx, gated, empty{}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
			})
			held := holdOn(t, opened, terminalTask)
			done := make(chan error, 1)
			go func() { done <- wait.run(opened, root, id) }()
			close(gate)
			expectWaitAfterSubscriber(t, held, done)
		})
	}
}
