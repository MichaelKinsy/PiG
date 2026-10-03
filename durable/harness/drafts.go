package harness

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// Document drafts are Chord overlay handles (packages/chord/src/delta/tracker.ts), so the operations a commit emits
// are exactly upstream's: a container assignment is one set, a push or splice one positional operation, and string
// growth an append. The helpers below translate upstream's draft mutations into handle calls.

// docDraft returns the draft of a document in a commit, creating it when absent.
func docDraft(tx durable.Tx, token durable.AnyDocToken, args ...any) (*delta.Object, error) {
	return tx.Doc(token, args...)
}

// setJSON assigns the JSON representation of value at key: one set of the whole value.
func setJSON(object *delta.Object, key string, value any) error {
	encoded, err := durable.ToJsonValue(value)
	if err != nil {
		return err
	}
	return object.Set(key, encoded)
}

// pushJSON appends the JSON representations of values.
func pushJSON(array *delta.Array, values ...any) error {
	encoded := make([]any, len(values))
	for index, value := range values {
		item, err := durable.ToJsonValue(value)
		if err != nil {
			return err
		}
		encoded[index] = item
	}
	_, err := array.Push(encoded...)
	return err
}

// plainValue returns the present content of a value read through a draft handle.
func plainValue(value any) any {
	switch typed := value.(type) {
	case *delta.Object:
		return typed.Snapshot()
	case *delta.Array:
		return typed.Snapshot()
	}
	return value
}

// decodeAt decodes the value at key as T; nil when absent.
func decodeAt[T any](object *delta.Object, key string) (*T, error) {
	value, present := object.Lookup(key)
	if !present {
		return nil, nil
	}
	decoded, err := durable.FromJsonValue[T](plainValue(value))
	if err != nil {
		return nil, err
	}
	return &decoded, nil
}

// decodeDraft decodes the whole draft as T.
func decodeDraft[T any](object *delta.Object) (T, error) {
	return durable.FromJsonValue[T](object.Snapshot())
}

// numberAt returns the number at key; 0 when absent.
func numberAt(object *delta.Object, key string) float64 {
	number, _ := object.Get(key).(float64)
	return number
}

func marshalPlain(value any) ([]byte, error) { return json.Marshal(value) }

// decodeJSON decodes a plain JSON value into target through its JSON codec.
func decodeJSON(value durable.JsonValue, target any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}
