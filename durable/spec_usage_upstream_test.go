package durable

import (
	"context"
	"testing"
)

// Ports packages/durable/test/spec-usage.test.ts, the root-package examples: the plan-mode and container documents
// (§7.2, §2.2), the Payment task with intent, effect, and outcome (§5.1), Follow, the table-read rules (§4), and the
// revoked draft (§3.4, over the plan-mode document instead of LiveDoc). The examples that use durable/harness and
// durable/tools are in durable/examples/spec_usage_upstream_test.go, which imports them.

type planMode struct {
	Enabled bool `json:"enabled"`
}

type container struct {
	Image string `json:"image"`
}

type charge struct {
	Phase string `json:"phase"`
	Key   string `json:"key,omitempty"`
}

type followCheckpoint struct {
	Phase string `json:"phase"`
}

type paymentResult struct {
	EntryId EntryId `json:"entryId"`
}

type receipt struct {
	Id string `json:"id"`
}

type paymentsService interface {
	Charge(key string) (receipt, error)
	Cancel(checkpoint charge) error
}

func specUsageExamples(session Session, conversationId ConversationId, payments paymentsService, newKey func() string, receiptEntry func(receipt) EntryDraft) (map[string]func(context.Context) error, Task[JsonValue, charge, paymentResult, struct{}]) {
	planModeDoc := DefineDoc(DocDefinition[planMode]{
		CommonDocDefinition: CommonDocDefinition[planMode]{Kind: "app.plan-mode", Version: 1},
		DocumentSemantics:   DocumentSemantics{Scope: ScopeConversation, History: HistoryLatest, Fork: ForkCurrent},
		Initial:             func() planMode { return planMode{Enabled: false} },
	})
	containerDoc := DefineDoc(DocDefinition[container]{
		CommonDocDefinition: CommonDocDefinition[container]{Kind: "app.container", Version: 1},
		DocumentSemantics:   DocumentSemantics{Scope: ScopeConversation, History: HistoryLatest, Fork: ForkCurrent},
		Initial:             func() container { return container{Image: "node:22"} },
	})

	type paymentRuntime = TaskRuntime[JsonValue, charge, paymentResult, struct{}]
	type paymentTask = RunningTask[JsonValue, charge, paymentResult]
	type next = NextTaskState[charge, paymentResult]
	payment := DefineTask(TaskDefinition[JsonValue, charge, paymentResult, struct{}]{
		Name:    "app.payment",
		Version: 1,
		Initial: func(JsonValue) charge { return charge{Phase: "prepare"} },
		Phases: map[string]PhaseHandler[JsonValue, charge, paymentResult, struct{}]{
			// Intent, effect, outcome.
			"prepare": func(ctx context.Context, _ paymentTask, runtime paymentRuntime) error {
				return runtime.Commit(ctx, func(Tx, paymentTask) (*next, error) {
					return &next{Status: TaskRunning, Checkpoint: &charge{Phase: "charge", Key: newKey()}}, nil
				})
			},
			"charge": func(ctx context.Context, task paymentTask, runtime paymentRuntime) error {
				paid, err := payments.Charge(task.State.Checkpoint.Key) // idempotent by key
				if err != nil {
					return err
				}
				return runtime.Commit(ctx, func(tx Tx, current paymentTask) (*next, error) {
					entry, err := tx.AppendEntry(current.ConversationId, receiptEntry(paid))
					if err != nil {
						return nil, err
					}
					return &next{Status: TaskTerminal, Outcome: &TaskOutcome[paymentResult]{Status: OutcomeCompleted, Result: &paymentResult{EntryId: entry.Id}}}, nil
				})
			},
		},
		// The abort handler decides the outcome; returning without one faults the task.
		Abort: func(ctx context.Context, task paymentTask, runtime paymentRuntime) error {
			if err := payments.Cancel(*task.State.Checkpoint); err != nil {
				return err
			}
			return runtime.Commit(ctx, func(Tx, paymentTask) (*next, error) {
				return &next{Status: TaskTerminal, Outcome: &TaskOutcome[paymentResult]{Status: OutcomeAborted, Reason: new("user")}}, nil
			})
		},
	})

	follow := DefineTask(TaskDefinition[JsonObject, followCheckpoint, JsonValue, struct{}]{
		Name:    "app.follow",
		Version: 1,
		Initial: func(JsonObject) followCheckpoint { return followCheckpoint{Phase: "follow"} },
		Phases: map[string]PhaseHandler[JsonObject, followCheckpoint, JsonValue, struct{}]{
			"follow": func(context.Context, RunningTask[JsonObject, followCheckpoint, JsonValue], TaskRuntime[JsonObject, followCheckpoint, JsonValue, struct{}]) error {
				return nil
			},
		},
		Abort: func(context.Context, RunningTask[JsonObject, followCheckpoint, JsonValue], TaskRuntime[JsonObject, followCheckpoint, JsonValue, struct{}]) error {
			return nil
		},
	})

	sequences := map[string]func(context.Context) error{
		// Section 7.1: /plan toggles this conversation's state.
		"planMode": func(ctx context.Context) error {
			_, err := session.Commit(ctx, func(tx Tx) (any, error) {
				draft, err := TxDoc(tx, planModeDoc, conversationId)
				if err != nil {
					return nil, err
				}
				return nil, draft.Set("enabled", true)
			})
			return err
		},
		// Section 2.2: absent means the conversation runs locally.
		"containerEnv": func(ctx context.Context) error {
			_, err := Snapshot(ctx, session, containerDoc, conversationId)
			return err
		},
		// Section 4: table reads before the first table write; documents stay usable.
		"tableRules": func(ctx context.Context) error {
			_, err := session.Commit(ctx, func(tx Tx) (any, error) {
				if _, err := tx.Conversation(conversationId); err != nil { // table read
					return nil, err
				}
				live, err := TxDoc(tx, planModeDoc, conversationId)
				if err != nil {
					return nil, err
				}
				if _, err := tx.AppendEntry(conversationId, EntryDraft{Kind: "note"}); err != nil { // first table write
					return nil, err
				}
				if err := live.Set("enabled", false); err != nil { // document mutation remains valid
					return nil, err
				}
				// further table writes are fine
				_, err = CreateTask(tx, follow, JsonObject{}, TaskOptions{Ownership: TaskOwnership{Kind: TaskOwnedByConversation}, ConversationId: &conversationId})
				return nil, err
			})
			return err
		},
		// Section 3.4: drafts are revoked after their commit.
		"revokedDraft": func(ctx context.Context) error {
			var escaped Draft[planMode]
			if _, err := session.Commit(ctx, func(tx Tx) (any, error) {
				var err error
				escaped, err = TxDoc(tx, planModeDoc, conversationId)
				return nil, err
			}); err != nil {
				return err
			}
			return escaped.Set("enabled", false) // panics with delta.ErrRevoked: the draft was revoked
		},
	}
	return sequences, payment
}

// spec-usage.test.ts:375
func TestSpecUsageCompilesTheRootExamples(t *testing.T) {
	sequences, payment := specUsageExamples(nil, ROOT_CONVERSATION_ID, nil, nil, nil)
	if len(sequences) != 4 || payment.AnyDefinition().Name != "app.payment" {
		t.Fatalf("examples = %d sequences, %s", len(sequences), payment.AnyDefinition().Name)
	}
}
