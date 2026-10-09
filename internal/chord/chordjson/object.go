package chordjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"iter"
	"slices"
)

// Object is a JSON object that keeps JavaScript's property order. Keys iterate in own-key order (Reflect.ownKeys, Object.keys): array-index keys first in ascending numeric order, then the other keys in insertion order. Setting an existing key keeps its position; deleting a key and setting it again moves it to the end, as in JavaScript.
//
// The zero Object is empty and ready to use. An Object is not safe for concurrent mutation.
type Object struct {
	entries []member
	// index maps a key to its entries position once the object outgrows a linear scan.
	index map[string]int
	// dead counts deleted entries still holding a position.
	dead int
	// indexKeys reports that an array-index key was inserted, so Keys must order them first.
	indexKeys bool
}

type member struct {
	key   string
	value any
	live  bool
}

// scanLimit is the entry count up to which lookups scan instead of keeping an index map.
const scanLimit = 8

// NewObject returns an empty object with room for capacity keys.
func NewObject(capacity int) *Object {
	if capacity <= 0 {
		return &Object{} // as Decode builds one, so two empty objects are reflect.DeepEqual
	}
	return &Object{entries: make([]member, 0, capacity)}
}

// ObjectOf returns an object holding pairs in order. pairs alternates keys and values; a repeated key keeps its first position and takes its last value.
func ObjectOf(pairs ...any) *Object {
	object := NewObject(len(pairs) / 2)
	for at := 0; at+1 < len(pairs); at += 2 {
		object.Set(pairs[at].(string), pairs[at+1])
	}
	return object
}

func (object *Object) find(key string) int {
	if object.index != nil {
		if at, ok := object.index[key]; ok {
			return at
		}
		return -1
	}
	for at := range object.entries {
		if entry := &object.entries[at]; entry.live && entry.key == key {
			return at
		}
	}
	return -1
}

// Len returns the number of keys.
func (object *Object) Len() int {
	if object == nil {
		return 0
	}
	return len(object.entries) - object.dead
}

// Get returns the value at key and whether the key is present.
func (object *Object) Get(key string) (any, bool) {
	if object == nil {
		return nil, false
	}
	if at := object.find(key); at >= 0 {
		return object.entries[at].value, true
	}
	return nil, false
}

// Value returns the value at key, or nil when the key is absent.
func (object *Object) Value(key string) any {
	value, _ := object.Get(key)
	return value
}

// Has reports whether key is present.
func (object *Object) Has(key string) bool {
	if object == nil {
		return false
	}
	return object.find(key) >= 0
}

// Set stores value at key. A new key goes after the existing keys.
func (object *Object) Set(key string, value any) {
	if at := object.find(key); at >= 0 {
		object.entries[at].value = value
		return
	}
	object.entries = append(object.entries, member{key: key, value: value, live: true})
	if object.index != nil {
		object.index[key] = len(object.entries) - 1
	} else if len(object.entries) > scanLimit {
		object.reindex()
	}
	if !object.indexKeys && isArrayIndexKey(key) {
		object.indexKeys = true
	}
}

// Delete removes key and reports whether it was present.
func (object *Object) Delete(key string) bool {
	if object == nil {
		return false
	}
	at := object.find(key)
	if at < 0 {
		return false
	}
	object.entries[at] = member{}
	object.dead++
	if object.index != nil {
		delete(object.index, key)
	}
	if object.dead > scanLimit && object.dead*2 > len(object.entries) {
		object.compact()
	}
	return true
}

func (object *Object) compact() {
	live := object.entries[:0]
	for _, entry := range object.entries {
		if entry.live {
			live = append(live, entry)
		}
	}
	clear(object.entries[len(live):])
	object.entries = live
	object.dead = 0
	object.index = nil
	if len(object.entries) > scanLimit {
		object.reindex()
	}
}

func (object *Object) reindex() {
	object.index = make(map[string]int, len(object.entries))
	for at, entry := range object.entries {
		if entry.live {
			object.index[entry.key] = at
		}
	}
}

// Clone returns a shallow copy with the same keys in the same order.
func (object *Object) Clone() *Object {
	out := NewObject(object.Len())
	for _, entry := range object.entries {
		if entry.live {
			out.entries = append(out.entries, entry)
		}
	}
	out.indexKeys = object.indexKeys
	if len(out.entries) > scanLimit {
		out.reindex()
	}
	return out
}

// Keys returns the keys in own-key order.
func (object *Object) Keys() []string {
	keys := make([]string, 0, object.Len())
	for key := range object.All() {
		keys = append(keys, key)
	}
	return keys
}

// All iterates the keys and values in own-key order.
func (object *Object) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		if object == nil {
			return
		}
		if !object.indexKeys {
			for _, entry := range object.entries {
				if entry.live && !yield(entry.key, entry.value) {
					return
				}
			}
			return
		}
		var indexed []int
		for at, entry := range object.entries {
			if entry.live && isArrayIndexKey(entry.key) {
				indexed = append(indexed, at)
			}
		}
		slices.SortFunc(indexed, func(left, right int) int {
			return compareOwnKeys(object.entries[left].key, object.entries[right].key)
		})
		for _, at := range indexed {
			if !yield(object.entries[at].key, object.entries[at].value) {
				return
			}
		}
		for _, entry := range object.entries {
			if entry.live && !isArrayIndexKey(entry.key) && !yield(entry.key, entry.value) {
				return
			}
		}
	}
}

// MarshalJSON encodes the object with its keys in own-key order, as JSON.stringify does.
func (object *Object) MarshalJSON() ([]byte, error) {
	if object == nil {
		return []byte("null"), nil
	}
	// The members are encoded without HTML escaping, as JSON.stringify writes them; an encoder that escapes HTML escapes this output itself.
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encodeMembers(&out, encoder, object); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func encodeMembers(out *bytes.Buffer, encoder *json.Encoder, object *Object) error {
	out.WriteByte('{')
	first := true
	for key, value := range object.All() {
		if !first {
			out.WriteByte(',')
		}
		first = false
		if err := encodeValue(out, encoder, key); err != nil {
			return err
		}
		out.WriteByte(':')
		if err := encodeValue(out, encoder, value); err != nil {
			return err
		}
	}
	out.WriteByte('}')
	return nil
}

func encodeValue(out *bytes.Buffer, encoder *json.Encoder, value any) error {
	switch typed := value.(type) {
	case *Object:
		if typed != nil {
			return encodeMembers(out, encoder, typed)
		}
	case []any:
		if typed != nil {
			out.WriteByte('[')
			for index, item := range typed {
				if index > 0 {
					out.WriteByte(',')
				}
				if err := encodeValue(out, encoder, item); err != nil {
					return err
				}
			}
			out.WriteByte(']')
			return nil
		}
	case string:
		if loneSurrogateAt(typed, 0) >= 0 {
			encodeLoneSurrogates(out, typed)
			return nil
		}
	}
	if err := encoder.Encode(value); err != nil {
		return err
	}
	out.Truncate(out.Len() - 1)
	return nil
}

// loneSurrogateAt returns the index of the next WTF-8 encoded UTF-16 surrogate (ED A0-BF 80-BF) at or after from, or
// -1. A Go string that carries JavaScript text holds a lone UTF-16 unit in this form.
func loneSurrogateAt(text string, from int) int {
	for index := from; index+2 < len(text); index++ {
		if text[index] == 0xED && text[index+1] >= 0xA0 && text[index+1] <= 0xBF && text[index+2] >= 0x80 && text[index+2] <= 0xBF {
			return index
		}
	}
	return -1
}

// encodeLoneSurrogates writes text with each WTF-8 surrogate as its \u escape, as JSON.stringify writes a lone
// surrogate, and the rest as encoding/json writes it.
func encodeLoneSurrogates(out *bytes.Buffer, text string) {
	out.WriteByte('"')
	start := 0
	for at := loneSurrogateAt(text, 0); at >= 0; at = loneSurrogateAt(text, start) {
		writeStringBody(out, text[start:at])
		unit := 0xD000 | rune(text[at+1]&0x3F)<<6 | rune(text[at+2]&0x3F)
		fmt.Fprintf(out, "\\u%04x", unit)
		start = at + 3
	}
	writeStringBody(out, text[start:])
	out.WriteByte('"')
}

func writeStringBody(out *bytes.Buffer, text string) {
	if text == "" {
		return
	}
	var scratch bytes.Buffer
	encoder := json.NewEncoder(&scratch)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(text)
	encoded := bytes.TrimSuffix(scratch.Bytes(), []byte("\n"))
	out.Write(encoded[1 : len(encoded)-1])
}

// UnmarshalJSON decodes a JSON object, keeping its keys in document order. Nested objects decode as *Object.
func (object *Object) UnmarshalJSON(data []byte) error {
	value, err := Decode(data)
	if err != nil {
		return err
	}
	decoded, ok := value.(*Object)
	if !ok {
		return &json.UnmarshalTypeError{Value: jsonKind(value), Type: objectType}
	}
	*object = *decoded
	return nil
}

// MovedKeys returns the fewest string keys whose removal and re-insertion, in target's order, turns an object whose keys are in current's order into one whose keys are in target's order: a JavaScript object keeps a key in place until it is deleted, and a key set again after its deletion follows the keys already present. Both lists hold the same keys in own-key order; array-index keys, which always lead in numeric order, never move.
func MovedKeys(current, target []string) []string {
	current = slices.DeleteFunc(slices.Clone(current), isArrayIndexKey)
	target = slices.DeleteFunc(slices.Clone(target), isArrayIndexKey)
	if slices.Equal(current, target) {
		return nil
	}
	// The kept keys are a prefix of target that current lists in the same order once the moved suffix is removed.
	for kept := len(target) - 1; kept >= 0; kept-- {
		moved := make(map[string]bool, len(target)-kept)
		for _, key := range target[kept:] {
			moved[key] = true
		}
		rest := slices.DeleteFunc(slices.Clone(current), func(key string) bool { return moved[key] })
		if slices.Equal(rest, target[:kept]) {
			return slices.Clone(target[kept:])
		}
	}
	return slices.Clone(target)
}
