package durable

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
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
//
// An assistant message is one such part that grows with a stream: a document that holds one (pi.live's partial)
// receives an operation per commit inside it. Its decoding is independent per member and per content block, so the
// operations inside it are checked by decoding the message with only the touched blocks, and an append to the text of
// a block, which no decoding validates, is not decoded at all (checkAssistantOps).

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
	ops, ok := checkAssistantOps[T](value, ops)
	if !ok {
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

// checkAssistantOps checks the operations of ops that lie inside an assistant message the document's type holds at a
// plain struct path, and returns the others for the part-wise check. ok is false when a checked decode fails, which the
// caller reports with the whole value's decode.
func checkAssistantOps[T any](value JsonObject, ops []Op) (rest []Op, ok bool) {
	root := reflect.TypeFor[T]()
	var message *delta.JsonObject
	var touchesMessage bool
	var blocks []int
	for _, op := range ops {
		path := delta.PathOf(op)
		consumed, found := assistantAt(root, path)
		if !found || consumed >= len(path) {
			rest = append(rest, op)
			continue
		}
		object, found := objectAt(value, path[:consumed])
		if !found || (message != nil && message != object) {
			rest = append(rest, op)
			continue
		}
		message = object
		verb := delta.Verb(op)
		relative := path[consumed:]
		if verb == "r" || verb == "m" || verb == "p" {
			rest = append(rest, op)
			continue
		}
		if name, isString := relative[0].(string); !isString || name != "content" {
			touchesMessage = true
			continue
		}
		if len(relative) < 2 {
			rest = append(rest, op)
			continue
		}
		index, isIndex := segmentIndex(relative[1])
		if !isIndex || len(relative) == 2 && verb == "d" {
			rest = append(rest, op)
			continue
		}
		content, _ := message.Value("content").([]any)
		if index >= len(content) {
			rest = append(rest, op)
			continue
		}
		if verb == "a" && appendsUnvalidatedText(relative[2:]) {
			continue
		}
		if !slices.Contains(blocks, index) {
			blocks = append(blocks, index)
		}
	}
	if message == nil || !touchesMessage && len(blocks) == 0 {
		return rest, true
	}
	slices.Sort(blocks)
	content, _ := message.Value("content").([]any)
	probe := delta.JsonObjectOf()
	for key, member := range message.All() {
		if key == "content" {
			continue
		}
		probe.Set(key, member)
	}
	checked := make([]any, 0, len(blocks))
	for _, index := range blocks {
		checked = append(checked, content[index])
	}
	if _, has := message.Get("content"); has {
		probe.Set("content", checked)
	}
	_, err := FromJsonValue[ai.AssistantMessage](probe)
	return rest, err == nil
}

// appendsUnvalidatedText reports whether an append at path, relative to a content block, grows the text of a text block, the thinking
// of a thinking block or a string inside the arguments of a tool call: no decoding of an assistant message validates what they hold.
func appendsUnvalidatedText(path []any) bool {
	if len(path) == 0 {
		return false
	}
	name, _ := path[0].(string)
	switch {
	case len(path) == 1:
		return name == "text" || name == "thinking"
	default:
		return name == "arguments"
	}
}

// assistantAt follows path from root through plain struct members, pointers and nothing else, and reports the assistant message type
// it is at and how many segments it consumed to reach it.
func assistantAt(root reflect.Type, path []any) (consumed int, found bool) {
	current := root
	for consumed = 0; ; consumed++ {
		for current.Kind() == reflect.Pointer {
			current = current.Elem()
		}
		if current == reflect.TypeFor[ai.AssistantMessage]() {
			return consumed, true
		}
		if consumed >= len(path) || current.Kind() != reflect.Struct || reflect.PointerTo(current).Implements(jsonUnmarshalerType) || current.Implements(jsonUnmarshalerType) {
			return 0, false
		}
		name, isString := path[consumed].(string)
		if !isString {
			return 0, false
		}
		member, ok := jsonMember(current, name)
		if !ok {
			return 0, false
		}
		current = member
	}
}

// jsonMember is the type of the member of struct type t that the JSON member name decodes into, when t has only plain members.
func jsonMember(t reflect.Type, name string) (reflect.Type, bool) {
	var found reflect.Type
	for field := range t.Fields() {
		if field.Anonymous {
			return nil, false
		}
		if !field.IsExported() {
			continue
		}
		tag, _ := field.Tag.Lookup("json")
		memberName, _, _ := strings.Cut(tag, ",")
		if memberName == "-" && tag == "-" {
			continue
		}
		if memberName == "" {
			memberName = field.Name
		}
		if memberName == name {
			found = field.Type
		}
	}
	return found, found != nil
}

// objectAt follows path through the objects of value.
func objectAt(value JsonObject, path []any) (*delta.JsonObject, bool) {
	current := value
	for _, segment := range path {
		name, isString := segment.(string)
		if !isString {
			return nil, false
		}
		next, isObject := current.Value(name).(*delta.JsonObject)
		if !isObject {
			return nil, false
		}
		current = next
	}
	return current, true
}
