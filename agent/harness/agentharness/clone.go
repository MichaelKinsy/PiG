package agentharness

import (
	"reflect"
	"unsafe"
)

// CloneHarnessEvent returns a deep copy of an event (upstream structuredClone
// of the event object). Maps, slices, pointers and interface values held by
// the payload are copied recursively, so a listener that mutates its event
// cannot affect another recipient or the emitter. Like structuredClone, the
// copy preserves the object graph: a map, slice or pointer reached twice is
// copied once and shared inside the clone, and cycles are reproduced rather
// than followed forever. Unexported struct fields are copied by value.
func CloneHarnessEvent(event HarnessEvent) HarnessEvent {
	clone := event
	if event.Payload != nil {
		cloner := graphCloner{seen: map[graphKey]reflect.Value{}}
		clone.Payload = cloner.copy(reflect.ValueOf(event.Payload)).Interface().(HarnessEventPayload)
	}
	return clone
}

// graphKey identifies one reference value: its type, address and, for
// slices, length (slices of one backing array with different lengths are
// distinct upstream arrays).
type graphKey struct {
	typ     reflect.Type
	pointer unsafe.Pointer
	length  int
}

type graphCloner struct {
	seen map[graphKey]reflect.Value
}

func (cloner *graphCloner) copy(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return value
		}
		key := graphKey{typ: value.Type(), pointer: value.UnsafePointer()}
		if copied, ok := cloner.seen[key]; ok {
			return copied
		}
		copied := reflect.New(value.Type().Elem())
		cloner.seen[key] = copied
		copied.Elem().Set(cloner.copy(value.Elem()))
		return copied
	case reflect.Interface:
		if value.IsNil() {
			return value
		}
		copied := reflect.New(value.Type()).Elem()
		copied.Set(cloner.copy(value.Elem()))
		return copied
	case reflect.Map:
		if value.IsNil() {
			return value
		}
		key := graphKey{typ: value.Type(), pointer: value.UnsafePointer()}
		if copied, ok := cloner.seen[key]; ok {
			return copied
		}
		copied := reflect.MakeMapWithSize(value.Type(), value.Len())
		cloner.seen[key] = copied
		iterator := value.MapRange()
		for iterator.Next() {
			copied.SetMapIndex(cloner.copy(iterator.Key()), cloner.copy(iterator.Value()))
		}
		return copied
	case reflect.Slice:
		if value.IsNil() {
			return value
		}
		copied := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		if value.Len() != 0 {
			key := graphKey{typ: value.Type(), pointer: value.UnsafePointer(), length: value.Len()}
			if existing, ok := cloner.seen[key]; ok {
				return existing
			}
			cloner.seen[key] = copied
		}
		for index := range value.Len() {
			copied.Index(index).Set(cloner.copy(value.Index(index)))
		}
		return copied
	case reflect.Array:
		copied := reflect.New(value.Type()).Elem()
		for index := range value.Len() {
			copied.Index(index).Set(cloner.copy(value.Index(index)))
		}
		return copied
	case reflect.Struct:
		copied := reflect.New(value.Type()).Elem()
		copied.Set(value)
		for index := range value.NumField() {
			if !copied.Field(index).CanSet() {
				continue
			}
			copied.Field(index).Set(cloner.copy(value.Field(index)))
		}
		return copied
	default:
		return value
	}
}
