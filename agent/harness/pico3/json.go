// Package pico3 is the experimental Pico3 agent kernel: durable records,
// commit-granular documents, a single-writer Session line, a task scheduler,
// the built-in turn kinds, and the watch view protocol.
//
// Every durable position is strict JSON. In Go a JSON value is nil, bool,
// float64, string, []any, or map[string]any, exactly what encoding/json
// produces when decoding into any. Ids and sequence numbers are int64 in
// records and float64 inside JSON documents, as they are JavaScript numbers
// upstream.
package pico3

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
)

// JsonValue is a strict JSON value.
type JsonValue = any

// JsonObject is a strict JSON object.
type JsonObject = map[string]any

// Id identifies a conversation, entry, task, or input. Ids share one
// session-wide space.
type Id = int64

// Seq is a commit sequence number.
type Seq = int64

// ToStored returns the JSON representation of value: what survives a JSON
// round trip. Optional fields vanish and numbers become float64.
func ToStored(value any) (JsonValue, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var stored any
	if err := json.Unmarshal(encoded, &stored); err != nil {
		return nil, err
	}
	return stored, nil
}

// mustStored is ToStored for values the kernel constructs itself; a failure is
// a programming defect.
func mustStored(value any) JsonValue {
	stored, err := ToStored(value)
	if err != nil {
		panic(fmt.Sprintf("pico3: value is not strict JSON: %v", err))
	}
	return stored
}

// storedObject converts a value whose JSON form is an object.
func storedObject(value any) JsonObject {
	object, _ := mustStored(value).(map[string]any)
	return object
}

// cloneJSON deep-copies a normalized JSON value.
func cloneJSON(value JsonValue) JsonValue {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = cloneJSON(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = cloneJSON(item)
		}
		return out
	default:
		return value
	}
}

// cloneObject deep-copies a JSON object; nil stays nil.
func cloneObject(object JsonObject) JsonObject {
	if object == nil {
		return nil
	}
	return cloneJSON(object).(map[string]any)
}

// jsonEqual compares two normalized JSON values structurally.
func jsonEqual(left, right JsonValue) bool {
	return reflect.DeepEqual(normalizeNumbers(left), normalizeNumbers(right))
}

// normalizeNumbers maps every Go integer or float to float64 so values built
// in Go compare equal to decoded JSON.
func normalizeNumbers(value JsonValue) JsonValue {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = normalizeNumbers(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = normalizeNumbers(item)
		}
		return out
	default:
		if number, ok := asFloat(value); ok {
			return number
		}
		return value
	}
}

// asFloat converts any Go number to float64.
func asFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	default:
		return 0, false
	}
}

// asID converts a JSON number to an Id; ok is false for non-integers.
func asID(value any) (Id, bool) {
	number, ok := asFloat(value)
	if !ok || number != math.Trunc(number) {
		return 0, false
	}
	return Id(number), true
}

// idList converts a JSON array of numbers to ids, skipping non-numbers.
func idList(value any) []Id {
	items, _ := value.([]any)
	out := make([]Id, 0, len(items))
	for _, item := range items {
		if id, ok := asID(item); ok {
			out = append(out, id)
		}
	}
	return out
}

// idsJSON converts ids to a JSON array.
func idsJSON(ids []Id) []any {
	out := make([]any, len(ids))
	for index, id := range ids {
		out[index] = float64(id)
	}
	return out
}

// str reads a string member; "" when absent or not a string.
func str(object JsonObject, key string) string {
	value, _ := object[key].(string)
	return value
}

// obj reads an object member; nil when absent or not an object.
func obj(object JsonObject, key string) JsonObject {
	value, _ := object[key].(map[string]any)
	return value
}

// arr reads an array member; nil when absent or not an array.
func arr(object JsonObject, key string) []any {
	value, _ := object[key].([]any)
	return value
}

// sortedKeys returns an object's keys in ascending order.
func sortedKeys(object JsonObject) []string {
	return slices.Sorted(maps.Keys(object))
}

// strictJSON validates value as strict JSON and returns its normalized copy.
func strictJSON(value any, what string) (JsonValue, error) {
	stored, err := ToStored(value)
	if err != nil {
		return nil, fmt.Errorf("%s returned a non-JSON value: %w", what, err)
	}
	return stored, nil
}
