package durabletest

import (
	"fmt"
	"reflect"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/telemetry"
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

// CheckRecordCarriers writes a task with a stored trace carrier and creation-time links, and a submission with a stored
// trace carrier (Pi 2ec55d80 packages/durable/src/types.ts: TaskRecord.trace/links, SubmissionRecord.trace), and reads them
// back through storage. The carriers are opaque and the records persist them as plain JSON members, so a record without
// them reads back without them.
func CheckRecordCarriers(storage durable.Storage) error {
	rootId := createRoot(storage)
	taskId := mint[durable.TaskId](storage)
	plainId := mint[durable.TaskId](storage)
	submissionId := mint[durable.SubmissionId](storage)
	trace := telemetry.TelemetryCarrier{"adapter": "opaque", "b": "2", "a": "1"}
	links := []telemetry.TelemetryCarrier{{"link": "first"}, {"link": "second", "x": "y"}}
	task := pendingTask(taskId, rootId, "ready")
	task.Trace, task.Links = trace, links
	plain := pendingTask(plainId, rootId, "ready")
	commit(storage,
		taskWrite(task),
		taskWrite(plain),
		submissionWrite(durable.SubmissionRecord{Id: submissionId, ConversationId: rootId, Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued, Trace: trace}),
	)
	storedTask := must(storage.Task(conformanceContext, taskId))
	if !reflect.DeepEqual(storedTask.Trace, trace) || !reflect.DeepEqual(storedTask.Links, links) {
		return fmt.Errorf("task reads back trace %v and links %v, want %v and %v", storedTask.Trace, storedTask.Links, trace, links)
	}
	storedPlain := must(storage.Task(conformanceContext, plainId))
	if storedPlain.Trace != nil || storedPlain.Links != nil {
		return fmt.Errorf("task without carriers reads back trace %v and links %v", storedPlain.Trace, storedPlain.Links)
	}
	storedSubmission := must(storage.Submission(conformanceContext, submissionId))
	if !reflect.DeepEqual(storedSubmission.Trace, trace) {
		return fmt.Errorf("submission reads back trace %v, want %v", storedSubmission.Trace, trace)
	}
	// The stored carrier is detached: changing a read does not change what the next read returns.
	storedTask.Trace["adapter"] = "changed"
	if again := must(storage.Task(conformanceContext, taskId)); !reflect.DeepEqual(again.Trace, trace) {
		return fmt.Errorf("a changed read leaked into storage: %v", again.Trace)
	}
	return nil
}
