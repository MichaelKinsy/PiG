// Ports packages/durable/src/harness/json.ts.

package harness

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

type planKind uint8

const (
	// planAppend: the string grew by Text since it was last assigned: append it, when the lengths agree.
	planAppend planKind = iota + 1
	// planKeep: leave the value as it is; the caller writes it.
	planKeep
	// planFields: plans for some members (string keys) or items (int indices) of a container.
	planFields
)

// AssignPlan says how AssignJson writes one value instead of assigning it leaf by leaf.
type AssignPlan struct {
	Kind   planKind
	Text   string
	Fields map[any]*AssignPlan
}

func (plan *AssignPlan) field(key any) *AssignPlan {
	if plan == nil || plan.Kind != planFields {
		return nil
	}
	return plan.Fields[key]
}

// AssignJson assigns value at target[key] leaf by leaf. Chord records a container assignment as one full set and only emits an append when a string leaf is reassigned with a longer string, so writing the partial whole would store and publish the complete message on every flush. That append costs a compare of the whole string, though; where plan says what a string member grew by, and the committed string plus that text has the new string's length, the text is appended with AppendText instead.
func AssignJson(target *delta.Object, key string, value durable.JsonValue, plan *AssignPlan) error {
	return assignSlot(objectSlot{target, key}, value, plan)
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

func assignSlot(slot jsonSlot, value durable.JsonValue, plan *AssignPlan) error {
	if plan != nil && plan.Kind == planKeep {
		return nil
	}
	current := slot.get()
	if object, ok := current.(*delta.Object); ok {
		if record, isRecord := value.(*delta.JsonObject); isRecord {
			for _, name := range object.Keys() {
				if !record.Has(name) {
					object.Delete(name)
				}
			}
			for name, item := range record.All() {
				if err := assignSlot(objectSlot{object, name}, item, plan.field(name)); err != nil {
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
				if err := assignSlot(objectSlot{object, name}, record[name], plan.field(name)); err != nil {
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
					if err := assignSlot(arraySlot{array, index}, item, plan.field(index)); err != nil {
						return err
					}
				} else if _, err := array.Push(item); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if plan != nil && plan.Kind == planAppend {
		if member, isMember := slot.(objectSlot); isMember {
			if before, isString := current.(string); isString {
				if after, isString := value.(string); isString && appendsTo(before, plan.Text, after) {
					return delta.AppendText(member.object, member.key, plan.Text)
				}
			}
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

// appendsTo reports whether after is before+text as JavaScript strings, judged by length as upstream does: a lone high surrogate that ends before and a lone low surrogate that starts text join into one character.
func appendsTo(before, text, after string) bool {
	if jsstring.JoinsSurrogates(before, text) {
		return len(before)+len(text)-2 == len(after)
	}
	return len(before)+len(text) == len(after)
}
