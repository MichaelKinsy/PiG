// Package ordered gives the dynamic JSON members of a decoded Go value the key order of their JSON text.
//
// Pi decodes a stored record with JSON.parse, so an object inside a dynamic member (a JsonValue such as a task's checkpoint or an entry's data) keeps the key order of the text. encoding/json decodes such a member into a Go map, which has no order. Restore replaces each dynamic member with the same text decoded into insertion-ordered objects.
package ordered

import (
	"encoding"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

var (
	unmarshalerType     = reflect.TypeFor[json.Unmarshaler]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
	dynamicTypes        sync.Map // reflect.Type -> bool
)

// HasDynamic reports whether a value of type t can hold a dynamic JSON member that Restore would replace.
func HasDynamic(t reflect.Type) bool {
	return hasDynamic(t, map[reflect.Type]bool{})
}

func hasDynamic(t reflect.Type, visiting map[reflect.Type]bool) bool {
	if cached, ok := dynamicTypes.Load(t); ok {
		return cached.(bool)
	}
	if visiting[t] {
		return false
	}
	visiting[t] = true
	result := false
	switch {
	case customDecoding(t):
	case t.Kind() == reflect.Interface:
		result = t.NumMethod() == 0
	case t.Kind() == reflect.Pointer, t.Kind() == reflect.Slice, t.Kind() == reflect.Array:
		result = hasDynamic(t.Elem(), visiting)
	case t.Kind() == reflect.Map:
		result = mapKeyDecodable(t.Key()) && hasDynamic(t.Elem(), visiting)
	case t.Kind() == reflect.Struct:
		for field := range fields(t) {
			if hasDynamic(field.Type, visiting) {
				result = true
				break
			}
		}
	}
	delete(visiting, t)
	// A false result found while an enclosing type is still being visited may have cut a cycle that leads to a dynamic member, so
	// only a true result or a complete walk is cached.
	if result || len(visiting) == 0 {
		dynamicTypes.Store(t, result)
	}
	return result
}

// customDecoding reports whether t decodes itself; its representation is its own.
func customDecoding(t reflect.Type) bool {
	if t.Kind() == reflect.Interface {
		return false
	}
	pointer := reflect.PointerTo(t)
	return t.Implements(unmarshalerType) || pointer.Implements(unmarshalerType) || pointer.Implements(textUnmarshalerType)
}

func mapKeyDecodable(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return !reflect.PointerTo(t).Implements(textUnmarshalerType)
	}
	return false
}

// Restore replaces every dynamic member of *target (a member whose static type is an empty interface) with the value at the same position in tree, the JSON text *target was decoded from, decoded with insertion-ordered objects (delta.DecodeJson). target must be a non-nil pointer. Members that decode themselves (json.Unmarshaler or encoding.TextUnmarshaler) keep their value. tree's values become part of *target, so the caller must not share tree.
func Restore(target any, tree any) {
	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Pointer || value.IsNil() || !HasDynamic(value.Type().Elem()) {
		return
	}
	restore(value.Elem(), tree)
}

func restore(value reflect.Value, tree any) {
	t := value.Type()
	if !HasDynamic(t) {
		return
	}
	switch t.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return
		}
		if tree == nil {
			value.SetZero()
			return
		}
		value.Set(reflect.ValueOf(tree))
	case reflect.Pointer:
		if !value.IsNil() {
			restore(value.Elem(), tree)
		}
	case reflect.Slice, reflect.Array:
		items, ok := tree.([]any)
		if !ok {
			return
		}
		for index := range min(value.Len(), len(items)) {
			restore(value.Index(index), items[index])
		}
	case reflect.Map:
		object, ok := tree.(*delta.JsonObject)
		if !ok || value.IsNil() {
			return
		}
		for key, item := range object.All() {
			mapKey, ok := decodeMapKey(key, t.Key())
			if !ok {
				continue
			}
			current := value.MapIndex(mapKey)
			if !current.IsValid() {
				continue
			}
			element := reflect.New(t.Elem()).Elem()
			element.Set(current)
			restore(element, item)
			value.SetMapIndex(mapKey, element)
		}
	case reflect.Struct:
		object, ok := tree.(*delta.JsonObject)
		if !ok {
			return
		}
		restoreStruct(value, object)
	}
}

func decodeMapKey(key string, t reflect.Type) (reflect.Value, bool) {
	switch t.Kind() {
	case reflect.String:
		return reflect.ValueOf(key).Convert(t), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		number, err := strconv.ParseInt(key, 10, t.Bits())
		if err != nil {
			return reflect.Value{}, false
		}
		return reflect.ValueOf(number).Convert(t), true
	default:
		number, err := strconv.ParseUint(key, 10, t.Bits())
		if err != nil {
			return reflect.Value{}, false
		}
		return reflect.ValueOf(number).Convert(t), true
	}
}

// restoreStruct matches object's keys to fields as encoding/json does: an exact name first, then a case-insensitive one; a later key for the same field wins.
func restoreStruct(value reflect.Value, object *delta.JsonObject) {
	byName := map[string]field{}
	for field := range fields(value.Type()) {
		byName[field.name] = field
	}
	matched := map[string]any{}
	for key, item := range object.All() {
		if _, exact := byName[key]; exact {
			matched[key] = item
			continue
		}
		for name := range byName {
			if strings.EqualFold(name, key) {
				matched[name] = item
				break
			}
		}
	}
	for name, item := range matched {
		field := byName[name]
		target, ok := fieldByIndex(value, field.index)
		if ok {
			restore(target, item)
		}
	}
}

// fieldByIndex walks an embedded path, stopping at a nil embedded pointer, which encoding/json left unallocated because no member of it was decoded.
func fieldByIndex(value reflect.Value, index []int) (reflect.Value, bool) {
	for depth, at := range index {
		if depth > 0 && value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return reflect.Value{}, false
			}
			value = value.Elem()
		}
		value = value.Field(at)
	}
	return value, true
}

type field struct {
	name  string
	index []int
	reflect.Type
}

// fields yields the JSON members of struct type t as encoding/json names them, embedded structs' members promoted.
func fields(t reflect.Type) func(yield func(field) bool) {
	return func(yield func(field) bool) {
		var walk func(t reflect.Type, prefix []int) bool
		walk = func(t reflect.Type, prefix []int) bool {
			for at := range t.NumField() {
				structField := t.Field(at)
				tag := structField.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, _, _ := strings.Cut(tag, ",")
				index := append(append([]int(nil), prefix...), at)
				fieldType := structField.Type
				if structField.Anonymous && name == "" {
					embedded := fieldType
					if embedded.Kind() == reflect.Pointer {
						embedded = embedded.Elem()
					}
					if embedded.Kind() == reflect.Struct {
						if !walk(embedded, index) {
							return false
						}
						continue
					}
				}
				if !structField.IsExported() {
					continue
				}
				if name == "" {
					name = structField.Name
				}
				if !yield(field{name: name, index: index, Type: fieldType}) {
					return false
				}
			}
			return true
		}
		walk(t, nil)
	}
}
