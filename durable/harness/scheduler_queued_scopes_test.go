package harness

import (
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Pi ea448f45 scheduler.ts #loadScopes (:680-693) returns only the conversations of queued inputs, and #reconcile withdraws
// in each one below a cancelled owner. A conversation whose only queued submission is a write, owned by a task that is then
// aborted, is not withdrawn in by the abort cascade (core oracle: testdata/sched/writequeue.json).
func TestTheCascadeDoesNotWithdrawInAConversationWithOnlyAQueuedWrite(t *testing.T) {
	log := &signalLog{}
	harness, root, _ := sfStarted(t, newControlledStorage(), log)
	scheduler := harness.(*harnessImpl).tasks
	var mu sync.Mutex
	var withdrawn []durable.ConversationId
	scheduler.mu.Lock()
	withdraw := scheduler.options.WithdrawInputs
	scheduler.options.WithdrawInputs = func(tx durable.Tx, id durable.ConversationId) error {
		mu.Lock()
		withdrawn = append(withdrawn, id)
		mu.Unlock()
		return withdraw(tx, id)
	}
	scheduler.mu.Unlock()
	type made struct {
		task         durable.TaskId
		conversation durable.ConversationId
	}
	created := must(durable.Commit(testContext, root, func(tx durable.Tx) (made, error) {
		task, err := durable.CreateTask(tx, waitingTask(log), struct{}{}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
		if err != nil {
			return made{}, err
		}
		conversation, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: task}})
		if err != nil {
			return made{}, err
		}
		_, err = tx.CreateSubmission(durable.SubmissionCreate{ConversationId: conversation.Id, Type: durable.SubmissionTypeWrite, Status: durable.SubmissionQueued})
		return made{task, conversation.Id}, err
	}))
	harness.Resume()
	if _, err := harness.AbortTask(testContext, created.task); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.WaitForTask(testContext, created.task); err != nil {
		t.Fatal(err)
	}
	if err := harness.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if slices.Contains(withdrawn, created.conversation) {
		t.Fatalf("withdrawn in %v, want no withdrawal in conversation %d: Pi ea448f45's #loadScopes returns only queued inputs' conversations", withdrawn, created.conversation)
	}
	mustClose(t, harness)
}
