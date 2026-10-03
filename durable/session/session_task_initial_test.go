package session_test

import (
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// erasedTask is a custom erased task definition, as a registry may hold.
type erasedTask struct{ definition *durable.AnyTaskDefinition }

func (task erasedTask) AnyDefinition() *durable.AnyTaskDefinition { return task.definition }

// Upstream transaction.ts createTask calls definition.initial(input) inside the Tx operation, so a throwing initial
// rejects that operation and the commit rolls back; then it mints the ID and copies the record, so a non-JSON
// checkpoint is rejected after the mint.
func TestSessionCreateTaskInitial(t *testing.T) {
	t.Run("a panicking typed initial rejects the commit and leaves the Session usable", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		failing := durable.DefineTask(durable.TaskDefinition[obj, obj, obj, workHooks]{
			Name:    "test.failing",
			Version: 1,
			Initial: func(obj) obj { panic("initial failed") },
		})
		commits := len(harness.Storage.Commits())
		err := tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := durable.CreateTask(tx, failing, obj{"path": "a"}, conversationOwned(conversationId))
			return err
		})
		expectErrorContains(t, err, "initial failed")
		if len(harness.Storage.Commits()) != commits {
			t.Fatal("a rejected createTask writes nothing")
		}
		createWorkTask(t, harness, conversationId, false)
	})

	t.Run("mints before rejecting a non-JSON checkpoint", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		task := erasedTask{&durable.AnyTaskDefinition{
			Name:    "test.nan",
			Version: 1,
			Initial: func(durable.JsonValue) (durable.JsonValue, error) { return math.NaN(), nil },
		}}
		mints := harness.Storage.MintCount()
		err := tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.CreateTaskErased(task, obj{"path": "a"}, conversationOwned(conversationId))
			return err
		})
		expectStrictJSON(t, err)
		if harness.Storage.MintCount() != mints+1 {
			t.Fatalf("mints %d, want %d", harness.Storage.MintCount(), mints+1)
		}
	})
}
