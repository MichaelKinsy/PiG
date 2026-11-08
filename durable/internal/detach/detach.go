// Package detach deep-copies the durable records that cross a storage or Harness boundary, so the copy shares no
// mutable memory with what it was read from.
package detach

import (
	"maps"
	"reflect"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// JSON deep-copies a JSON value. JSON containers and scalars take the fast path; any other Go value is copied
// structurally.
func JSON(value durable.JsonValue) durable.JsonValue {
	switch typed := value.(type) {
	case nil, bool, string, float64, int, int64:
		return value
	case *delta.JsonObject:
		if typed == nil {
			return typed
		}
		out := delta.NewJsonObject(typed.Len())
		for key, item := range typed.All() {
			out.Set(key, JSON(item))
		}
		return out
	case map[string]any:
		if typed == nil {
			return typed
		}
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = JSON(item)
		}
		return out
	case []any:
		if typed == nil {
			return typed
		}
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = JSON(item)
		}
		return out
	default:
		return Value(reflect.ValueOf(value)).Interface()
	}
}

func Object(value durable.JsonObject) durable.JsonObject {
	if value == nil {
		return nil
	}
	out, _ := Document(value).(*delta.JsonObject)
	return out
}

// Document deep-copies a document value or operation. Document objects are ordered, so a Go map a caller placed in
// one becomes a *delta.JsonObject in JavaScript own-key order: a Go map has no insertion order to keep.
func Document(value durable.JsonValue) durable.JsonValue {
	switch typed := value.(type) {
	case *delta.JsonObject:
		if typed == nil {
			return typed
		}
		out := delta.NewJsonObject(typed.Len())
		for key, item := range typed.All() {
			out.Set(key, Document(item))
		}
		return out
	case map[string]any:
		if typed == nil {
			return typed
		}
		out := delta.NewJsonObject(len(typed))
		for _, key := range chordjson.MapOwnKeys(typed) {
			out.Set(key, Document(typed[key]))
		}
		return out
	case []any:
		if typed == nil {
			return typed
		}
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = Document(item)
		}
		return out
	}
	return JSON(value)
}

// Value deep-copies an arbitrary Go value: pointers, slices, maps, interfaces, and the exported fields of structs.
func Value(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return value
		}
		out := reflect.New(value.Type().Elem())
		out.Elem().Set(Value(value.Elem()))
		return out
	case reflect.Interface:
		if value.IsNil() {
			return value
		}
		out := reflect.New(value.Type()).Elem()
		out.Set(Value(value.Elem()))
		return out
	case reflect.Slice:
		if value.IsNil() {
			return value
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := range value.Len() {
			out.Index(index).Set(Value(value.Index(index)))
		}
		return out
	case reflect.Map:
		if value.IsNil() {
			return value
		}
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			out.SetMapIndex(iterator.Key(), Value(iterator.Value()))
		}
		return out
	case reflect.Array:
		out := reflect.New(value.Type()).Elem()
		for index := range value.Len() {
			out.Index(index).Set(Value(value.Index(index)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(value.Type()).Elem()
		out.Set(value)
		for index := range value.NumField() {
			if out.Field(index).CanSet() {
				out.Field(index).Set(Value(value.Field(index)))
			}
		}
		return out
	default:
		return value
	}
}

func Pointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func Messages(messages []ai.Message) []ai.Message {
	if messages == nil {
		return nil
	}
	out := make([]ai.Message, len(messages))
	for index, message := range messages {
		if message == nil {
			continue
		}
		out[index] = Value(reflect.ValueOf(message)).Interface().(ai.Message)
	}
	return out
}

func Entry(record durable.EntryRecord) durable.EntryRecord {
	record.Model = Messages(record.Model)
	record.Data = JSON(record.Data)
	record.Head = Pointer(record.Head)
	record.ByTaskId = Pointer(record.ByTaskId)
	if record.Edits != nil {
		edits := make([]durable.ContextEdit, len(record.Edits))
		for index, edit := range record.Edits {
			edit.Messages = Messages(edit.Messages)
			edits[index] = edit
		}
		record.Edits = edits
	}
	return record
}

// ContextView deep-copies a context view.
func ContextView(view durable.ContextView) durable.ContextView {
	out := durable.ContextView{Head: nil, Messages: Messages(view.Messages)}
	if view.Head != nil {
		head := Entry(*view.Head)
		out.Head = &head
	}
	if view.Entries != nil {
		out.Entries = make([]durable.EntryRecord, len(view.Entries))
		for index := range view.Entries {
			out.Entries[index] = Entry(view.Entries[index])
		}
	}
	if view.Contributions != nil {
		out.Contributions = make([][]ai.Message, len(view.Contributions))
		for index, contribution := range view.Contributions {
			out.Contributions[index] = Messages(contribution)
		}
	}
	return out
}

// Conversation copies a conversation record.
func Conversation(record durable.ConversationRecord) durable.ConversationRecord {
	record.Parent = Pointer(record.Parent)
	record.Owner = Pointer(record.Owner)
	return record
}

func jsonPointer(value *durable.JsonValue) *durable.JsonValue {
	if value == nil {
		return nil
	}
	copied := JSON(*value)
	return &copied
}

func carrier(value telemetry.TelemetryCarrier) telemetry.TelemetryCarrier {
	if value == nil {
		return nil
	}
	return maps.Clone(value)
}

// Task deep-copies a task record.
func Task(record durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
	record.Trace = carrier(record.Trace)
	if record.Links != nil {
		links := make([]telemetry.TelemetryCarrier, len(record.Links))
		for index, link := range record.Links {
			links[index] = carrier(link)
		}
		record.Links = links
	}
	record.Input = JSON(record.Input)
	record.Owner = Pointer(record.Owner)
	record.StartedAt = Pointer(record.StartedAt)
	record.EndedAt = Pointer(record.EndedAt)
	state := record.State
	state.Checkpoint = jsonPointer(state.Checkpoint)
	state.On = slices.Clone(state.On)
	state.Until = Pointer(state.Until)
	if state.Outcome != nil {
		outcome := *state.Outcome
		outcome.Result = jsonPointer(outcome.Result)
		if outcome.Error != nil {
			taskError := *outcome.Error
			taskError.Detail = JSON(taskError.Detail)
			outcome.Error = &taskError
		}
		outcome.Reason = Pointer(outcome.Reason)
		state.Outcome = &outcome
	}
	record.State = state
	if record.Memos != nil {
		memos := make(map[string]durable.JsonValue, len(record.Memos))
		for key, value := range record.Memos {
			memos[key] = JSON(value)
		}
		record.Memos = memos
	}
	return record
}

// Submission deep-copies a submission record.
func Submission(record durable.SubmissionRecord) durable.SubmissionRecord {
	record.Trace = carrier(record.Trace)
	record.RequestId = Pointer(record.RequestId)
	record.Entry = Pointer(record.Entry)
	record.Answer = Pointer(record.Answer)
	record.Reason = Pointer(record.Reason)
	record.Detail = JSON(record.Detail)
	return record
}

// DocumentRecord copies a document incarnation's lifecycle record.
func DocumentRecord(record durable.DocumentRecord) durable.DocumentRecord {
	record.Key = Pointer(record.Key)
	record.RetiredAt = Pointer(record.RetiredAt)
	return record
}
