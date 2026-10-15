package durabletest

import (
	"fmt"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// CheckRecordKeyOrder writes records whose dynamic members (entry data, task input and checkpoint, submission detail) hold objects
// with keys out of alphabetical order, then reads them back through storage. Pi decodes stored records with JSON.parse, so each object
// keeps its key order; a Go map would list the keys sorted.
func CheckRecordKeyOrder(storage durable.Storage) error {
	rootId := createRoot(storage)
	entryId := mint[durable.EntryId](storage)
	taskId := mint[durable.TaskId](storage)
	submissionId := mint[durable.SubmissionId](storage)
	object := func() *chorddelta.JsonObject {
		return chorddelta.JsonObjectOf("zeta", 1, "alpha", chorddelta.JsonObjectOf("yak", 2, "bee", 3), "mid", []any{chorddelta.JsonObjectOf("x", 4, "c", 5)})
	}
	const want = `{"zeta":1,"alpha":{"yak":2,"bee":3},"mid":[{"x":4,"c":5}]}`
	task := pendingTask(taskId, rootId, "ready")
	task.Input = object()
	task.State.Checkpoint = new(durable.JsonValue(object()))
	commit(storage,
		entryWrite(entryWith(entry(entryId, rootId, "note"), func(record *durable.EntryRecord) { record.Data = object() })),
		taskWrite(task),
		submissionWrite(durable.SubmissionRecord{Id: submissionId, ConversationId: rootId, Type: durable.SubmissionTypeInput, Status: durable.SubmissionUnanswered, Reason: new("failed"), Detail: object()}),
	)
	storedEntry := must(storage.Entry(conformanceContext, entryId))
	storedTask := must(storage.Task(conformanceContext, taskId))
	storedSubmission := must(storage.Submission(conformanceContext, submissionId))
	for name, value := range map[string]any{
		"entry data":        storedEntry.Entry.Data,
		"task input":        storedTask.Input,
		"task checkpoint":   *storedTask.State.Checkpoint,
		"submission detail": storedSubmission.Detail,
	} {
		//portlint:allow jsonescape the text is compared only for key order, against keys without <, > or &
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if string(encoded) != want {
			return fmt.Errorf("%s reads back as %s, want %s", name, encoded, want)
		}
	}
	return nil
}
