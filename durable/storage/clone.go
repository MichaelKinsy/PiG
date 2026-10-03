package storage

import (
	"reflect"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/internal/writes"
)

// Storage keeps values detached from callers: every write is copied before it is retained and every read returns a
// copy. Upstream also freezes retained writes; Go has no frozen values, so a prepared commit exposes copies instead.

// cloneJSON deep-copies a JSON value. JSON containers and scalars take the fast path; any other Go value is copied
// structurally.
func cloneJSON(value durable.JsonValue) durable.JsonValue {
	switch typed := value.(type) {
	case nil, bool, string, float64, int, int64:
		return value
	case map[string]any:
		if typed == nil {
			return typed
		}
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = cloneJSON(item)
		}
		return out
	case []any:
		if typed == nil {
			return typed
		}
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = cloneJSON(item)
		}
		return out
	default:
		return cloneValue(reflect.ValueOf(value)).Interface()
	}
}

func cloneObject(value durable.JsonObject) durable.JsonObject {
	if value == nil {
		return nil
	}
	out, _ := cloneJSON(value).(map[string]any)
	return out
}

// cloneValue deep-copies an arbitrary Go value: pointers, slices, maps, interfaces, and the exported fields of structs.
func cloneValue(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return value
		}
		out := reflect.New(value.Type().Elem())
		out.Elem().Set(cloneValue(value.Elem()))
		return out
	case reflect.Interface:
		if value.IsNil() {
			return value
		}
		out := reflect.New(value.Type()).Elem()
		out.Set(cloneValue(value.Elem()))
		return out
	case reflect.Slice:
		if value.IsNil() {
			return value
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := range value.Len() {
			out.Index(index).Set(cloneValue(value.Index(index)))
		}
		return out
	case reflect.Map:
		if value.IsNil() {
			return value
		}
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			out.SetMapIndex(iterator.Key(), cloneValue(iterator.Value()))
		}
		return out
	case reflect.Array:
		out := reflect.New(value.Type()).Elem()
		for index := range value.Len() {
			out.Index(index).Set(cloneValue(value.Index(index)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(value.Type()).Elem()
		out.Set(value)
		for index := range value.NumField() {
			if out.Field(index).CanSet() {
				out.Field(index).Set(cloneValue(value.Field(index)))
			}
		}
		return out
	default:
		return value
	}
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func cloneMessages(messages []ai.Message) []ai.Message {
	if messages == nil {
		return nil
	}
	out := make([]ai.Message, len(messages))
	for index, message := range messages {
		if message == nil {
			continue
		}
		out[index] = cloneValue(reflect.ValueOf(message)).Interface().(ai.Message)
	}
	return out
}

func cloneConversation(record durable.ConversationRecord) durable.ConversationRecord {
	record.Parent = clonePointer(record.Parent)
	record.Owner = clonePointer(record.Owner)
	return record
}

func cloneEntry(record durable.EntryRecord) durable.EntryRecord {
	record.Model = cloneMessages(record.Model)
	record.Data = cloneJSON(record.Data)
	record.Head = clonePointer(record.Head)
	record.ByTaskId = clonePointer(record.ByTaskId)
	if record.Edits != nil {
		edits := make([]durable.ContextEdit, len(record.Edits))
		for index, edit := range record.Edits {
			edit.Messages = cloneMessages(edit.Messages)
			edits[index] = edit
		}
		record.Edits = edits
	}
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
		copied, _ := cloneJSON([]any(op)).([]any)
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
