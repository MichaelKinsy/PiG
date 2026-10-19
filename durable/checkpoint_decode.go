package durable

import (
	"encoding/json"
	"reflect"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// A document definition's typed CheckpointWhen receives the whole prepared value decoded as T, and fails the commit
// when that decode fails. CheckpointWhenRaw skips the decode, which is the cost that grew with the document, so the
// commit still has to fail exactly where the typed decode would have. Decoding a JSON object into a struct decodes
// each field on its own, and every value a document holds was either built from a T (creation, migration, a fork's
// copy) or passed this check as the delta that produced it, so a committed value decodes and only what the delta's
// operations changed can fail. checkDecodes decodes those parts alone, and when any of them fails, or the
// operations are not of a shape it understands, decodes the whole value, which returns the error the typed
// predicate returned.

var jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()

// partTouch is what a batch of operations changed under one top-level field.
type partTouch struct {
	whole      bool     // the field's whole value changed
	indexes    []int    // array elements changed in place
	keys       []string // object members changed
	items      []any    // array elements an operation spliced in
	structural bool     // an array element was deleted, spliced or reordered
}

// checkDecodes returns the error FromJsonValue[T](value) returns, assuming value minus the changes ops describe
// decodes as T.
func checkDecodes[T any](value JsonObject, ops []Op) error {
	if reflect.TypeFor[*T]().Implements(jsonUnmarshalerType) || reflect.TypeFor[T]().Implements(jsonUnmarshalerType) {
		_, err := FromJsonValue[T](value)
		return err
	}
	touched, order, ok := touchedParts(ops)
	if ok {
		for _, field := range order {
			if !partsDecode[T](value, field, touched[field]) {
				ok = false
				break
			}
		}
	}
	if ok {
		return nil
	}
	_, err := FromJsonValue[T](value)
	return err
}

// touchedParts groups ops by top-level field; ok is false for an operation that cannot be attributed to one.
func touchedParts(ops []Op) (map[string]*partTouch, []string, bool) {
	touched := map[string]*partTouch{}
	var order []string
	for _, op := range ops {
		verb := delta.Verb(op)
		path := delta.PathOf(op)
		if verb == "r" || len(path) == 0 {
			return nil, nil, false
		}
		field, isString := path[0].(string)
		if !isString {
			return nil, nil, false
		}
		part := touched[field]
		if part == nil {
			part = &partTouch{}
			touched[field] = part
			order = append(order, field)
		}
		if len(path) == 1 {
			switch verb {
			case "d":
			case "s", "a", "t":
				part.whole = true
			case "m":
				part.structural = true
			case "p":
				items, ok := splicedItems(op)
				if !ok {
					return nil, nil, false
				}
				part.structural = true
				part.items = append(part.items, items...)
			default:
				return nil, nil, false
			}
			continue
		}
		switch segment := path[1].(type) {
		case string:
			if verb != "d" || len(path) > 2 {
				part.keys = append(part.keys, segment)
			}
		default:
			index, ok := segmentIndex(segment)
			if !ok {
				return nil, nil, false
			}
			if verb == "d" && len(path) == 2 {
				part.structural = true
			} else {
				part.indexes = append(part.indexes, index)
			}
		}
	}
	return touched, order, true
}

// splicedItems is the items of a ["p", path, index, remove, items] operation.
func splicedItems(op Op) ([]any, bool) {
	if len(op) != 5 {
		return nil, false
	}
	items, ok := op[4].([]any)
	return items, ok
}

func segmentIndex(segment any) (int, bool) {
	switch number := segment.(type) {
	case int:
		return number, number >= 0
	case int64:
		return int(number), number >= 0
	case float64:
		index := int(number)
		return index, number >= 0 && float64(index) == number
	}
	return 0, false
}

// partsDecode reports whether the changed parts of one top-level field decode as T.
func partsDecode[T any](value JsonObject, field string, part *partTouch) bool {
	final, present := value.Get(field)
	if !present {
		return true
	}
	decodes := func(piece any) bool {
		_, err := FromJsonValue[T](delta.JsonObjectOf(field, piece))
		return err == nil
	}
	if part.whole {
		return decodes(final)
	}
	switch container := final.(type) {
	case []any:
		if len(part.keys) > 0 || (part.structural && len(part.indexes) > 0) {
			return decodes(final)
		}
		for _, index := range slices.Compact(slices.Sorted(slices.Values(part.indexes))) {
			if index >= len(container) {
				return decodes(final)
			}
			if !decodes([]any{container[index]}) {
				return false
			}
		}
		if len(part.items) > 0 && !decodes(part.items) {
			return false
		}
		return true
	case *delta.JsonObject:
		if len(part.indexes) > 0 || part.structural || len(part.items) > 0 {
			return decodes(final)
		}
		for _, key := range part.keys {
			member, present := container.Get(key)
			if !present {
				continue
			}
			if !decodes(delta.JsonObjectOf(key, member)) {
				return false
			}
		}
		return true
	}
	return decodes(final)
}
