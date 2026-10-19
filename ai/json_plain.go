package ai

import (
	"bytes"
	"encoding"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// plainWalk is the containers on the path from the root to the value being visited. A container met again below itself
// makes the tree cyclic, which the marshaling path reports.
type plainWalk struct {
	// inline holds the first levels of a path, so a shallow tree allocates no path and the walk itself stays on the caller's stack. overflow holds the levels below.
	inline   [8]uintptr
	depth    int
	overflow []uintptr
	// validatedStrings skips the string check for a tree whose strings an earlier clone already checked.
	validatedStrings bool
	// knownValid is a string an earlier clone of the same growing value found to be valid UTF-8: a string that begins with it is checked from where it ends. longest is the longest string of at least rememberedStringLength bytes that this walk found valid UTF-8, which the caller hands to its next clone as knownValid.
	knownValid string
	longest    string
}

// rememberedStringLength is the length from which a string a walk found valid is worth remembering: shorter ones cost less to check again than to compare.
const rememberedStringLength = 256

// validString is isWTF8(text) for a walk that carries knownValid: only what follows a known valid prefix is read. A valid UTF-8 text that is longer than the longest one the walk has remembered is remembered.
func (walk *plainWalk) validString(text string) bool {
	var strict bool
	if known := walk.knownValid; known != "" && len(text) >= len(known) && strings.HasPrefix(text, known) {
		// known is valid UTF-8 and ends between two runes, so the text is valid UTF-8 exactly when what follows it is.
		strict = utf8.ValidString(text[len(known):])
	} else {
		strict = utf8.ValidString(text)
	}
	if strict {
		if len(text) >= rememberedStringLength && len(text) > len(walk.longest) {
			walk.longest = text
		}
		return true
	}
	return isWTF8(text)
}

// enterSlice records a non-empty slice by its first element's address, which names its backing array without boxing the slice; it reports false when the slice is already on the path.
func (walk *plainWalk) enterSlice(container []any) bool {
	return walk.push(reflect.ValueOf(&container[0]).Pointer())
}

// enterMap records a non-empty map; it reports false when the map is already on the path.
func (walk *plainWalk) enterMap(container map[string]any) bool {
	return walk.push(reflect.ValueOf(container).Pointer())
}

func (walk *plainWalk) push(pointer uintptr) bool {
	if slices.Contains(walk.inline[:min(walk.depth, len(walk.inline))], pointer) || slices.Contains(walk.overflow, pointer) {
		return false
	}
	if walk.depth < len(walk.inline) {
		walk.inline[walk.depth] = pointer
	} else {
		walk.overflow = append(walk.overflow, pointer)
	}
	walk.depth++
	return true
}

func (walk *plainWalk) leave() {
	walk.depth--
	if walk.depth >= len(walk.inline) {
		walk.overflow = walk.overflow[:len(walk.overflow)-1]
	}
}

// clonePlainJSON deep-copies a tree built only of nil, bool, string, finite numbers, raw JSON, []any, map[string]any and
// JsonObject, the shape decoded JSON and tool arguments have. It returns exactly what cloneJSONValue returns for such a
// tree: normalizeJSONValue's marshal-and-decode round trip changes nothing in it except that strings with invalid
// UTF-8 would be rewritten, preserveJSONValueTypes restores every native number, and it keeps raw JSON as its compact
// copy. ok is false for any other value; the caller then takes the marshaling path.
func clonePlainJSON(value any, walk *plainWalk) (copied any, ok bool) {
	switch typed := value.(type) {
	case nil:
		return nil, true
	case bool:
		return value, true
	case string:
		return value, walk.validatedStrings || walk.validString(typed)
	case float64:
		return value, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case float32:
		return value, !math.IsNaN(float64(typed)) && !math.IsInf(float64(typed), 0)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, uintptr:
		return value, true
	case json.RawMessage:
		return cloneRawJSON(typed)
	case map[string]any:
		if typed == nil {
			return typed, true
		}
		return clonePlainObject(typed, walk)
	case JsonObject:
		if typed == nil {
			return map[string]any(nil), true
		}
		return clonePlainObject(typed, walk)
	case []any:
		if typed == nil {
			return typed, true
		}
		if len(typed) > 0 {
			if !walk.enterSlice(typed) {
				return nil, false
			}
			defer walk.leave()
		}
		out := make([]any, len(typed))
		for index, item := range typed {
			if out[index], ok = clonePlainJSON(item, walk); !ok {
				return nil, false
			}
		}
		return out, true
	default:
		return nil, false
	}
}

// isPlainRawJSON reports whether raw JSON marshals without error: the encoder writes a non-empty raw value after checking it, and the decoder reads back whatever that check accepts. Empty raw JSON, which the encoder writes as null or rejects, is left to the marshaling path.
func isPlainRawJSON(raw json.RawMessage) bool {
	return len(raw) > 0 && sdkjson.Valid(raw)
}

// cloneRawJSON is cloneJSONValue for raw JSON: after the round trip proves it valid, preserveJSONValueTypes keeps the compact copy of the original bytes rather than the decoded tree, so its members keep their order. Tool result details read back from a session are raw JSON, and every turn's context copies them; without this, each copy encoded, compacted and decoded all of them.
func cloneRawJSON(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	// Compact scans with the checker isPlainRawJSON uses and fails where it does, so the copy is also the check.
	var compact bytes.Buffer
	if sdkjson.Compact(&compact, raw) != nil {
		return nil, false
	}
	return json.RawMessage(compact.Bytes()), true
}

func clonePlainObject(object map[string]any, walk *plainWalk) (any, bool) {
	if len(object) > 0 {
		if !walk.enterMap(object) {
			return nil, false
		}
		defer walk.leave()
	}
	out := make(map[string]any, len(object))
	for key, item := range object {
		if !utf8.ValidString(key) {
			return nil, false
		}
		copied, ok := clonePlainJSON(item, walk)
		if !ok {
			return nil, false
		}
		out[key] = copied
	}
	return out, true
}

// isPlainJSON reports whether value is a plain tree that marshals without error; false means the marshaling path must
// decide.
func isPlainJSON(value any, walk *plainWalk) bool {
	switch typed := value.(type) {
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, uintptr:
		return true
	case string:
		return isWTF8(typed)
	case json.RawMessage:
		return isPlainRawJSON(typed)
	case float64:
		return !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case float32:
		return !math.IsNaN(float64(typed)) && !math.IsInf(float64(typed), 0)
	case map[string]any:
		return isPlainObject(typed, walk)
	case JsonObject:
		return isPlainObject(typed, walk)
	case []any:
		if len(typed) > 0 {
			if !walk.enterSlice(typed) {
				return false
			}
			defer walk.leave()
		}
		for _, item := range typed {
			if !isPlainJSON(item, walk) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func isPlainObject(object map[string]any, walk *plainWalk) bool {
	if len(object) > 0 {
		if !walk.enterMap(object) {
			return false
		}
		defer walk.leave()
	}
	for key, item := range object {
		if !utf8.ValidString(key) || !isPlainJSON(item, walk) {
			return false
		}
	}
	return true
}

// alwaysMarshalsTypes caches typeAlwaysMarshals per type.
var alwaysMarshalsTypes sync.Map // reflect.Type -> bool

// typeAlwaysMarshals reports whether every value of t marshals through sdkjson without error into valid JSON, so
// validating one needs no encoding: a typed tool result's details (a struct of strings, bools, integers and pointers to
// such structs) is valid by construction. sdkjson writes ill-formed UTF-8 as U+FFFD, so any string qualifies. A float
// (NaN and ±Inf fail), an interface, a map, a type with its own marshaling or IsZero method, and a type that contains
// itself (a pointer cycle fails) do not.
func typeAlwaysMarshals(t reflect.Type) bool {
	if cached, ok := alwaysMarshalsTypes.Load(t); ok {
		return cached.(bool)
	}
	result := typeAlwaysMarshalsWalk(t, nil)
	alwaysMarshalsTypes.Store(t, result)
	return result
}

var (
	sdkMarshalerType  = reflect.TypeFor[sdkjson.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	isZeroerType      = reflect.TypeFor[interface{ IsZero() bool }]()
)

func typeAlwaysMarshalsWalk(t reflect.Type, path []reflect.Type) bool {
	if slices.Contains(path, t) {
		return false
	}
	for _, method := range []reflect.Type{sdkMarshalerType, textMarshalerType, isZeroerType} {
		if t.Implements(method) || reflect.PointerTo(t).Implements(method) {
			return false
		}
	}
	switch t.Kind() {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return typeAlwaysMarshalsWalk(t.Elem(), append(path, t))
	case reflect.Struct:
		path = append(path, t)
		for field := range t.Fields() {
			if !field.Anonymous && !field.IsExported() || field.Tag.Get("json") == "-" {
				continue
			}
			if !typeAlwaysMarshalsWalk(field.Type, path) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// equalPlainJSON reports whether a and b are plain JSON trees (the shape clonePlainJSON accepts) that json.Marshal writes
// as the same bytes. It answers false for anything else, including a tree it cannot vouch for, and the caller then
// decides by marshaling. Strings and map keys compare as bytes, since equal inputs marshal alike, and floats compare by
// bit pattern so +0 and -0, which marshal differently, stay apart. A nil map or slice differs from an empty one, as
// "null" differs from "{}". A cycle in a, which marshaling reports, is left to the
// marshaling path.
func equalPlainJSON(a, b any, walk *plainWalk) bool {
	switch left := a.(type) {
	case nil:
		return b == nil
	case bool:
		right, ok := b.(bool)
		return ok && left == right
	case string:
		right, ok := b.(string)
		return ok && left == right
	case float64:
		right, ok := b.(float64)
		return ok && !math.IsNaN(left) && !math.IsInf(left, 0) && math.Float64bits(left) == math.Float64bits(right)
	case float32:
		right, ok := b.(float32)
		return ok && !math.IsNaN(float64(left)) && !math.IsInf(float64(left), 0) && math.Float32bits(left) == math.Float32bits(right)
	case int:
		right, ok := b.(int)
		return ok && left == right
	case int8:
		right, ok := b.(int8)
		return ok && left == right
	case int16:
		right, ok := b.(int16)
		return ok && left == right
	case int32:
		right, ok := b.(int32)
		return ok && left == right
	case int64:
		right, ok := b.(int64)
		return ok && left == right
	case uint:
		right, ok := b.(uint)
		return ok && left == right
	case uint8:
		right, ok := b.(uint8)
		return ok && left == right
	case uint16:
		right, ok := b.(uint16)
		return ok && left == right
	case uint32:
		right, ok := b.(uint32)
		return ok && left == right
	case uint64:
		right, ok := b.(uint64)
		return ok && left == right
	case uintptr:
		right, ok := b.(uintptr)
		return ok && left == right
	case map[string]any:
		switch right := b.(type) {
		case map[string]any:
			return equalPlainObjects(left, right, walk)
		case JsonObject:
			return equalPlainObjects(left, right, walk)
		}
		return false
	case JsonObject:
		switch right := b.(type) {
		case map[string]any:
			return equalPlainObjects(left, right, walk)
		case JsonObject:
			return equalPlainObjects(left, right, walk)
		}
		return false
	case []any:
		right, ok := b.([]any)
		if !ok || (left == nil) != (right == nil) || len(left) != len(right) {
			return false
		}
		if len(left) > 0 {
			if !walk.enterSlice(left) {
				return false
			}
			defer walk.leave()
		}
		for index := range left {
			if !equalPlainJSON(left[index], right[index], walk) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func equalPlainObjects(left, right map[string]any, walk *plainWalk) bool {
	if (left == nil) != (right == nil) || len(left) != len(right) {
		return false
	}
	if len(left) > 0 {
		if !walk.enterMap(left) {
			return false
		}
		defer walk.leave()
	}
	for key, value := range left {
		other, ok := right[key]
		if !ok || !equalPlainJSON(value, other, walk) {
			return false
		}
	}
	return true
}

// isWTF8 reports whether text is UTF-8 in which the only other sequences are the three-byte forms of a lone UTF-16 surrogate, the way a Go string holds a JavaScript string that has one. Such text is JSON the wire writes as the escape and reads back unchanged, so a clone keeps it; any other invalid byte is replaced when the value is marshaled.
func isWTF8(text string) bool {
	if utf8.ValidString(text) {
		return true
	}
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		switch {
		case r != utf8.RuneError || size != 1:
			i += size
		case i+2 < len(text) && text[i] == 0xED && text[i+1] >= 0xA0 && text[i+1] <= 0xBF && text[i+2] >= 0x80 && text[i+2] <= 0xBF:
			i += 3
		default:
			return false
		}
	}
	return true
}
