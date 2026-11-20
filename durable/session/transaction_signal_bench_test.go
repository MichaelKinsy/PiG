package session_test

// pi: packages/durable/src/session/transaction.ts

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// BenchmarkSignalDelivery is the cost of one signal delivery to a committed task in a commit of its own: the read of the task,
// the ticket that orders deliveries started together, and the decision. The key holds a value after the first delivery, so every
// later one answers duplicate and stages no write: the cost measured is the delivery's, not the growth of the task's memos.
func BenchmarkSignalDelivery(b *testing.B) {
	harness := open()
	conversationId := sessiontest.CreateConversation(b, harness.Session)
	taskId, err := durable.Commit(ctx, harness.Session, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, workTask, delta.JsonObjectOf("path", "a"), conversationOwned(conversationId))
	})
	if err != nil {
		b.Fatal(err)
	}
	deliver := func() durable.SignalResult {
		result, err := durable.Commit(ctx, harness.Session, func(tx durable.Tx) (durable.SignalResult, error) {
			return durable.Signal(tx, taskId, turnsToken, "a", turnsAnswer{Text: "x"})
		})
		if err != nil {
			b.Fatal(err)
		}
		return result
	}
	if got := deliver(); got != durable.SignalDelivered {
		b.Fatalf("first delivery: %s", got)
	}
	b.ReportAllocs()
	for b.Loop() {
		if got := deliver(); got != durable.SignalDuplicate {
			b.Fatalf("delivery: %s", got)
		}
	}
}
