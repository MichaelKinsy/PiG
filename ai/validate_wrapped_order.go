package ai

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// wrappedObject is an object that TypeBox's Convert replaced with an array holding it (FromArray turns a value that is not an array into [value]), once for each array it was wrapped in.
// The object keeps the member order it had in the arguments' text, which readSchemaObjectOrder recorded at the path the object had before it moved one element deeper.
// object keeps the map reachable, so its address is not reused by another map during the validation.
type wrappedObject struct {
	object  map[string]any
	pointer uintptr
	count   int
}

func (s *argumentSchema) noteWrappedObject(object map[string]any) {
	root := s.root
	pointer := reflect.ValueOf(object).Pointer()
	if i := slices.IndexFunc(root.wrapped, func(w wrappedObject) bool { return w.pointer == pointer }); i >= 0 {
		root.wrapped[i].count++
		return
	}
	root.wrapped = append(root.wrapped, wrappedObject{object: object, pointer: pointer, count: 1})
}

// relocateWrappedOrder moves the member order recorded for each wrapped object, and for the objects below it, from the path the object had in the arguments' text to its path in value. Walking value from the root visits an outer object before the objects inside it, so
// each move starts from paths the moves before it already made current.
func relocateWrappedOrder(order schemaObjectOrder, value any, wrapped []wrappedObject) schemaObjectOrder {
	if len(order) == 0 || len(wrapped) == 0 {
		return order
	}
	type move struct {
		path  string
		count int
	}
	var moves []move
	var walk func(value any, path string)
	walk = func(value any, path string) {
		switch typed := value.(type) {
		case map[string]any:
			pointer := reflect.ValueOf(typed).Pointer()
			if i := slices.IndexFunc(wrapped, func(w wrappedObject) bool { return w.pointer == pointer }); i >= 0 {
				moves = append(moves, move{path, wrapped[i].count})
			}
			for key, item := range typed {
				walk(item, schemaPath(path, key))
			}
		case []any:
			for i, item := range typed {
				walk(item, schemaPath(path, strconv.Itoa(i)))
			}
		}
	}
	walk(value, "")
	for _, m := range moves {
		from := m.path
		for range m.count {
			trimmed, ok := strings.CutSuffix(from, "/0")
			if !ok {
				break
			}
			from = trimmed
		}
		if from == m.path {
			continue
		}
		moved := make(schemaObjectOrder, len(order))
		for recorded, keys := range order {
			if recorded == from || strings.HasPrefix(recorded, from+"/") {
				recorded = m.path + recorded[len(from):]
			}
			moved[recorded] = keys
		}
		order = moved
	}
	return order
}

// marshalInSourceOrder is MarshalJSONInSourceOrder for arguments that Convert may have rearranged.
func (s *argumentSchema) marshalInSourceOrder(value any, args []byte) ([]byte, error) {
	return marshalInRecordedOrder(value, s.sourceOrder(value, args))
}

// sourceOrder is the member order of args' text at the paths value has after Convert.
func (s *argumentSchema) sourceOrder(value any, args []byte) schemaObjectOrder {
	order, err := readSchemaObjectOrder(args)
	if err != nil {
		return nil
	}
	return relocateWrappedOrder(order, value, s.wrapped)
}
