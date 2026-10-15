package sessionentry

import (
	"bytes"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// Pi casts whatever JSON.parse returns for a line to an entry and checks no member (session-manager.ts parseSessionEntryLine), so an entry's
// type, id and parentId are any JavaScript value: a record that is not an object has them undefined, and an object may carry a number. byId,
// getTree's nodeMap and the leaf compare them as Map keys (SameValueZero), and getBranch and buildSessionPath stop at a falsy one.
//
// A Go entry keeps such a value in its string field as a key that no JSON string decodes to (decoded strings are valid UTF-8 or WTF-8, so
// never hold the byte 0xFF): equal primitive values have equal keys, each parsed object or array has a key of its own, and a string keeps
// itself.
const jsValueMark = "\xff"

const (
	jsUndefined = jsValueMark
	jsNumber    = jsValueMark + "n"
	jsOther     = jsValueMark + "j"
)

// jsValueKey is the key of the JavaScript value JSON.parse makes of raw, a member that is not a string; nil raw is an absent member (undefined).
func jsValueKey(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0:
		return jsUndefined
	case raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9':
		return jsNumber + jsnumber.String(jsnumber.FromJSON(raw))
	}
	canonical, err := jsonstringify.Canonicalize(raw)
	if err != nil {
		return jsUndefined
	}
	if raw[0] == '{' || raw[0] == '[' {
		// Map keys compare objects by identity: each parse makes a new one, which no other entry's member equals.
		return jsOther + string(canonical) + "\x00" + strconv.FormatUint(objectKeys.Add(1), 10)
	}
	return jsOther + string(canonical)
}

// sameKey reports whether two keys stand for the same value apart from the identity jsValueKey gives each parsed object.
func sameKey(a, b string) bool {
	if a == b {
		return true
	}
	if !strings.HasPrefix(a, jsOther) || !strings.HasPrefix(b, jsOther) {
		return false
	}
	valueA, _, objectA := strings.Cut(a, "\x00")
	valueB, _, objectB := strings.Cut(b, "\x00")
	return objectA && objectB && valueA == valueB
}

// objectKeys numbers the object and array values jsValueKey has keyed.
var objectKeys atomic.Uint64

// IsJSValue reports whether id is the key of a non-string JavaScript value rather than a string.
func IsJSValue(id string) bool { return strings.HasPrefix(id, jsValueMark) }

// Truthy reports whether the value id stands for is truthy, as getBranch's `startId ?` and the walks' `current.parentId ?` test it: the empty
// string, undefined, null, false and zero are falsy.
func Truthy(id string) bool {
	switch id {
	case "", jsUndefined, jsNumber + "0", jsNumber + "NaN", jsOther + "null", jsOther + "false":
		return false
	}
	return true
}

// IsNull reports whether id is the key of null: an entry whose id is null leaves Pi's leafId null.
func IsNull(id string) bool { return id == jsOther+"null" }

// ValueJSON is what JSON.stringify writes for the value id stands for; ok is false for undefined, whose member JSON.stringify omits.
func ValueJSON(id string) (value json.RawMessage, ok bool) {
	switch {
	case !IsJSValue(id):
		encoded, err := json.Marshal(id)
		return encoded, err == nil
	case id == jsUndefined:
		return nil, false
	case strings.HasPrefix(id, jsNumber):
		return jsnumber.JSON(jsnumber.Parse(id[len(jsNumber):])), true
	}
	text, _, _ := strings.Cut(id[len(jsOther):], "\x00")
	return json.RawMessage(text), true
}

// lenientBase reads the base members of a record the strict decode rejects: an id or parentId that is a string keeps its value and any other
// is its JavaScript value's key (an absent one is undefined, a null parentId stays nil); a type or timestamp that is not a string is empty, as no
// entry type or date matches it.
// data is the record with those members removed, for decoding the entry type's other members; nil when raw is not an object.
func lenientBase(raw json.RawMessage) (base SessionEntryBase, data json.RawMessage) {
	base = SessionEntryBase{ID: jsUndefined, raw: raw}
	undefined := jsUndefined
	base.ParentID = &undefined
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil || members == nil {
		return base, nil
	}
	member := func(name string) string {
		value, present := members[name]
		delete(members, name)
		if !present {
			return jsUndefined
		}
		var text string
		if json.Unmarshal(value, &text) == nil && bytes.HasPrefix(bytes.TrimSpace(value), []byte(`"`)) {
			return text
		}
		return jsValueKey(value)
	}
	if typ := member("type"); !IsJSValue(typ) {
		base.Type = typ
	}
	base.ID = member("id")
	switch parent := member("parentId"); {
	case IsNull(parent):
		base.ParentID = nil
	default:
		base.ParentID = &parent
	}
	if timestamp := member("timestamp"); !IsJSValue(timestamp) {
		base.Timestamp = timestamp
	}
	//portlint:allow mapkeyorder data is only decoded into the entry type's struct (decodeMember), member values stay raw, and the record keeps its own bytes (base.raw)
	data, err := json.Marshal(members)
	if err != nil {
		return base, nil
	}
	return base, data
}

// WireID is id as a host-call string: a string id is itself, an undefined or null one is "", as a null leaf is, and any other value is the text
// JSON.stringify writes for it.
func WireID(id string) string {
	if !IsJSValue(id) {
		return id
	}
	if value, ok := ValueJSON(id); ok && !IsNull(id) {
		return string(value)
	}
	return ""
}
