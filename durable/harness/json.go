// Ports packages/durable/src/harness/json.ts.

package harness

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// AssignJson assigns value at target[key] leaf by leaf. Chord records a container assignment as one full set and only emits an append when a string leaf is reassigned with a longer string, so writing the partial whole would store and publish the complete message on every flush.
func AssignJson(target *delta.Object, key string, value durable.JsonValue) error {
	return assignSlot(objectSlot{target, key}, value)
}

// jsonSlot is one assignable position: an object member or an array element.
type jsonSlot interface {
	get() any
	set(value any) error
}

type objectSlot struct {
	object *delta.Object
	key    string
}

func (slot objectSlot) get() any            { return slot.object.Get(slot.key) }
func (slot objectSlot) set(value any) error { return slot.object.Set(slot.key, value) }

type arraySlot struct {
	array *delta.Array
	index int
}

func (slot arraySlot) get() any            { return slot.array.Get(slot.index) }
func (slot arraySlot) set(value any) error { return slot.array.Set(slot.index, value) }

func assignSlot(slot jsonSlot, value durable.JsonValue) error {
	current := slot.get()
	if object, ok := current.(*delta.Object); ok {
		if record, isRecord := value.(*delta.JsonObject); isRecord {
			for _, name := range object.Keys() {
				if !record.Has(name) {
					object.Delete(name)
				}
			}
			for name, item := range record.All() {
				if err := assignSlot(objectSlot{object, name}, item); err != nil {
					return err
				}
			}
			return nil
		}
		// A Go map has no insertion order; it is a record whose members are visited in own-key order, as Chord copies one at its boundary.
		if record, isRecord := value.(map[string]any); isRecord {
			for _, name := range object.Keys() {
				if _, has := record[name]; !has {
					object.Delete(name)
				}
			}
			for _, name := range chordjson.MapOwnKeys(record) {
				if err := assignSlot(objectSlot{object, name}, record[name]); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if array, ok := current.(*delta.Array); ok {
		if items, isArray := value.([]any); isArray && array.Len() <= len(items) {
			for index, item := range items {
				if index < array.Len() {
					if err := assignSlot(arraySlot{array, index}, item); err != nil {
						return err
					}
				} else if _, err := array.Push(item); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if isLeaf(current) && isLeaf(value) && current == value {
		return nil
	}
	return slot.set(value)
}

// isLeaf reports whether value is a JSON scalar, which compares by value as upstream's !== does for primitives.
func isLeaf(value any) bool {
	switch value.(type) {
	case nil, bool, float64, string, int, int64:
		return true
	}
	return false
}

// jsonMember reads key from a decoded JSON object. Document values are *delta.JsonObject; a task record's
// durable.JsonValue fields (checkpoint, input, outcome) decode through encoding/json as map[string]any.
func jsonMember(value any, key string) any {
	switch object := value.(type) {
	case *delta.JsonObject:
		return object.Value(key)
	case map[string]any:
		return object[key]
	}
	return nil
}
