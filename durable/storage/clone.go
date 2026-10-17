package storage

import (
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/internal/detach"
	"github.com/MichaelKinsy/PiG/durable/storage/internal/writes"
)

// Storage keeps values detached from callers: every write is copied before it is retained and every read returns a
// copy. Upstream also freezes retained writes; Go has no frozen values, so a prepared commit exposes copies instead.

func cloneConversation(record durable.ConversationRecord) durable.ConversationRecord {
	record.Parent = clonePointer(record.Parent)
	record.Owner = clonePointer(record.Owner)
	return record
}

func cloneJSONPointer(value *durable.JsonValue) *durable.JsonValue {
	if value == nil {
		return nil
	}
	copied := cloneJSON(*value)
	return &copied
}

func cloneTask(record storedTask) storedTask {
	record.Input = cloneJSON(record.Input)
	record.Owner = clonePointer(record.Owner)
	state := record.State
	state.Checkpoint = cloneJSONPointer(state.Checkpoint)
	if state.On != nil {
		state.On = append([]durable.TaskId(nil), state.On...)
	}
	if state.Outcome != nil {
		outcome := *state.Outcome
		outcome.Result = cloneJSONPointer(outcome.Result)
		if outcome.Error != nil {
			taskError := *outcome.Error
			taskError.Detail = cloneJSON(taskError.Detail)
			outcome.Error = &taskError
		}
		outcome.Reason = clonePointer(outcome.Reason)
		state.Outcome = &outcome
	}
	record.State = state
	if record.Memos != nil {
		memos := make(map[string]durable.JsonValue, len(record.Memos))
		for key, value := range record.Memos {
			memos[key] = cloneJSON(value)
		}
		record.Memos = memos
	}
	return record
}

func cloneSubmission(record durable.SubmissionRecord) durable.SubmissionRecord {
	record.RequestId = clonePointer(record.RequestId)
	record.Entry = clonePointer(record.Entry)
	record.Answer = clonePointer(record.Answer)
	record.Reason = clonePointer(record.Reason)
	record.Detail = cloneJSON(record.Detail)
	return record
}

func cloneDocumentCreate(record durable.DocumentCreate) durable.DocumentCreate {
	record.Key = clonePointer(record.Key)
	return record
}

func cloneDocumentRecord(record durable.DocumentRecord) durable.DocumentRecord {
	record.Key = clonePointer(record.Key)
	record.RetiredAt = clonePointer(record.RetiredAt)
	return record
}

func cloneOps(ops []durable.Op) []durable.Op {
	if ops == nil {
		return nil
	}
	out := make([]durable.Op, len(ops))
	for index, op := range ops {
		copied, _ := detach.Document([]any(op)).([]any)
		out[index] = durable.Op(copied)
	}
	return out
}

func cloneContent(content durable.DocumentContent) durable.DocumentContent {
	content.Value = cloneObject(content.Value)
	content.Ops = cloneOps(content.Ops)
	return content
}

// cloneWrite returns the value form of write sharing no mutable state with it.
func cloneWrite(write durable.StorageWrite) durable.StorageWrite {
	switch typed := writes.Value(write).(type) {
	case durable.ConversationWrite:
		return durable.ConversationWrite{Value: cloneConversation(typed.Value)}
	case durable.EntryWrite:
		return durable.EntryWrite{Value: cloneEntry(typed.Value)}
	case durable.TaskWrite:
		return durable.TaskWrite{Value: cloneTask(typed.Value)}
	case durable.SubmissionWrite:
		return durable.SubmissionWrite{Value: cloneSubmission(typed.Value)}
	case durable.DocumentCreateWrite:
		return durable.DocumentCreateWrite{Record: cloneDocumentCreate(typed.Record), Content: cloneContent(typed.Content)}
	case durable.DocumentCopyWrite:
		return durable.DocumentCopyWrite{Record: cloneDocumentCreate(typed.Record), Source: typed.Source}
	case durable.DocumentChangeWrite:
		return durable.DocumentChangeWrite{Id: typed.Id, Content: cloneContent(typed.Content)}
	default:
		return typed
	}
}

// cloneWrites returns writes that share no mutable state with the given batch.
func cloneWrites(batch []durable.StorageWrite) []durable.StorageWrite {
	out := make([]durable.StorageWrite, len(batch))
	for index, write := range batch {
		out[index] = cloneWrite(write)
	}
	return out
}

func cloneJSON(value durable.JsonValue) durable.JsonValue       { return detach.JSON(value) }
func cloneObject(value durable.JsonObject) durable.JsonObject   { return detach.Object(value) }
func cloneEntry(record durable.EntryRecord) durable.EntryRecord { return detach.Entry(record) }
func clonePointer[T any](value *T) *T                           { return detach.Pointer(value) }
