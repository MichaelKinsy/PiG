package sdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The members of pi-ai's `Type` that compute a schema (Pick, Omit, Partial, Required, KeyOf, Index, Mapped, Conditional, Extends, Evaluate, Instantiate, Cyclic, Module, Script, ...) are
// served by TypeBox's type engine, which the host runs: one port of TypeBox's build/type/{action,engine,extends,script} for every SDK (internal/typebox/engine). A call encodes its
// arguments in the engine's wire form, asks the host (the type.engine call, which the host answers before the extension registers, so a schema built in a package variable works), and
// decodes the schema the host returns: the enumerable members in the order TypeBox wrote them and the hidden ones (`~kind`, `~optional`, ...). A fused extension runs the engine itself
// through [HostHelpers.TypeEngine].
const callTypeEngine = "type.engine"

// wireEnvelope is the key of an envelope in the wire form (see the engine's wire.go).
const wireEnvelope = "\x00"

// bigIntStringifyError is what JSON.stringify throws for a bigint (a V8 TypeError); a schema that holds one fails with it where Pi's value fails: when it is written as JSON.
const bigIntStringifyError = "Do not know how to serialize a BigInt"

// BigInt is a JavaScript bigint a schema holds as a member, such as the `const` of `Type.Script("1n")` or `Type.Literal(BigInt)`: TypeBox's value holds it, and only
// `JSON.stringify` of it fails. Writing a schema that holds one as JSON (MarshalJSON, registering it as a tool's parameters) fails with Pi's message, "Do not know how to serialize
// a BigInt"; every other use of the value (the guards, passing it to another member) works.
type BigInt struct{ value *big.Int }

// NewBigInt is the bigint of value, which it copies.
func NewBigInt(value *big.Int) BigInt { return BigInt{new(big.Int).Set(value)} }

// Int returns the bigint's value as a new *big.Int; the zero BigInt is 0n.
func (b BigInt) Int() *big.Int {
	if b.value == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(b.value)
}

// String is the digits of the bigint, as JavaScript's `String(1n)` writes them.
func (b BigInt) String() string {
	if b.value == nil {
		return "0"
	}
	return b.value.String()
}

// MarshalJSON always fails with JSON.stringify's message for a bigint: the value has no JSON form.
func (BigInt) MarshalJSON() ([]byte, error) { return nil, errors.New(bigIntStringifyError) }

// typeEngineCall runs member of the engine on args and returns the value it answers; what TypeBox throws panics with its message, as Pi's `Type` throws.
func typeEngineCall(member string, args ...any) any {
	var refs []Refinement
	var request bytes.Buffer
	request.WriteString(`{"member":`)
	writeWireString(&request, member)
	request.WriteString(`,"args":[`)
	for i, arg := range args {
		if i > 0 {
			request.WriteByte(',')
		}
		wireEncode(&request, arg, &refs)
	}
	request.WriteString(`]}`)
	reply, err := typeEngineTransport(request.Bytes())
	if err != nil {
		panic(fmt.Sprintf("Type.%s: %v", member, err))
	}
	reader := &wireReader{text: string(reply), refs: refs}
	root := reader.value()
	envelope, _ := root.(*OrderedObject)
	if envelope == nil {
		panic(fmt.Sprintf("Type.%s: the host answered %s", member, reply))
	}
	if failure, failed := envelope.Get("error"); failed {
		details, _ := failure.(*OrderedObject)
		message, _ := details.Get("message")
		panic(fmt.Sprint(message))
	}
	value, _ := envelope.Get("value")
	return value
}

// typeEngineTransport sends one engine request to the host (or to the engine itself in a fused extension) and returns its reply.
func typeEngineTransport(request []byte) ([]byte, error) {
	if hook := installedHelpers(); hook != nil && hook.TypeEngine != nil {
		return hook.TypeEngine(request), nil
	}
	result, err := helperCall(callTypeEngine, sdkjson.RawMessage(request))
	if err := callResultError(result, err); err != nil {
		return nil, err
	}
	return result.Result, nil
}

// engineSchema is a result that is an object as a schema: a plain object (Module's result, an Extends result's members) has no hidden members.
func engineSchema(value any) *TSchema {
	switch value := value.(type) {
	case *TSchema:
		return value
	case *OrderedObject:
		return &TSchema{object: value, hidden: map[string]any{}}
	}
	panic(fmt.Sprintf("TypeError: the engine answered %T, not a schema", value))
}

// ---- wire: encode ----

func writeWireString(b *bytes.Buffer, s string) {
	encoded, _ := sdkjson.MarshalStringify(s)
	b.Write(encoded)
}

func wireEncode(b *bytes.Buffer, value any, refs *[]Refinement) {
	switch value := value.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(value))
	case string:
		writeWireString(b, value)
	case BigInt:
		b.WriteString(`{"\u0000":"n","v":"` + value.String() + `"}`)
	case *big.Int:
		b.WriteString(`{"\u0000":"n","v":"` + value.String() + `"}`)
	case *TSchema:
		if value == nil {
			b.WriteString("null")
			return
		}
		if value.items != nil {
			wireEncode(b, value.items, refs)
			return
		}
		hidden := make([]string, 0, len(value.hidden))
		for name := range value.hidden {
			hidden = append(hidden, name)
		}
		slices.SortFunc(hidden, func(a, c string) int {
			switch {
			case a == c:
				return 0
			case a == "~kind":
				return -1
			case c == "~kind":
				return 1
			}
			return strings.Compare(a, c)
		})
		wireObject(b, hidden, value.hidden, value.object.keys, value.object.values, refs)
	case *OrderedObject:
		if value == nil {
			b.WriteString("{}")
			return
		}
		wireObject(b, nil, nil, value.keys, value.values, refs)
	case map[string]any:
		keys := sortedKeys(value)
		wireObject(b, nil, nil, keys, value, refs)
	case []any:
		b.WriteByte('[')
		for i, item := range value {
			if i > 0 {
				b.WriteByte(',')
			}
			wireEncode(b, item, refs)
		}
		b.WriteByte(']')
	case []string:
		b.WriteByte('[')
		for i, item := range value {
			if i > 0 {
				b.WriteByte(',')
			}
			writeWireString(b, item)
		}
		b.WriteByte(']')
	case []*TSchema:
		items := make([]any, len(value))
		for i, item := range value {
			items[i] = item
		}
		wireEncode(b, items, refs)
	case []Refinement:
		b.WriteByte('[')
		for i, refinement := range value {
			if i > 0 {
				b.WriteByte(',')
			}
			*refs = append(*refs, refinement)
			b.WriteString(`{"\u0000":"f","i":` + strconv.Itoa(len(*refs)-1) + `}`)
		}
		b.WriteByte(']')
	default:
		if number, ok := typeNumber(value); ok {
			encoded, _ := sdkjson.MarshalStringify(number)
			b.Write(encoded)
			return
		}
		if reflected := reflect.ValueOf(value); reflected.Kind() == reflect.Slice {
			items := make([]any, reflected.Len())
			for i := range items {
				items[i] = reflected.Index(i).Interface()
			}
			wireEncode(b, items, refs)
			return
		}
		panic(fmt.Sprintf("TypeError: a %T has no JavaScript counterpart a schema can hold", value))
	}
}

// wireObject writes an object whose hidden members come first in the envelope and whose enumerable members follow, in order.
func wireObject(b *bytes.Buffer, hiddenKeys []string, hidden map[string]any, keys []string, values map[string]any, refs *[]Refinement) {
	envelope := len(hiddenKeys) > 0
	for _, key := range keys {
		if strings.HasPrefix(key, wireEnvelope) {
			envelope = true
		}
	}
	if envelope {
		b.WriteString(`{"\u0000":"o"`)
		if len(hiddenKeys) > 0 {
			b.WriteString(`,"h":{`)
			for i, key := range hiddenKeys {
				if i > 0 {
					b.WriteByte(',')
				}
				writeWireString(b, key)
				b.WriteByte(':')
				wireEncode(b, hidden[key], refs)
			}
			b.WriteByte('}')
		}
		b.WriteString(`,"e":`)
	}
	b.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		writeWireString(b, key)
		b.WriteByte(':')
		wireEncode(b, values[key], refs)
	}
	b.WriteByte('}')
	if envelope {
		b.WriteByte('}')
	}
}

// ---- wire: decode ----

type wireReader struct {
	text string
	pos  int
	refs []Refinement
}

func (r *wireReader) fail(what string) {
	panic(fmt.Sprintf("Type: bad reply from the host: %s at %d", what, r.pos))
}

func (r *wireReader) space() {
	for r.pos < len(r.text) && strings.IndexByte(" \t\r\n", r.text[r.pos]) >= 0 {
		r.pos++
	}
}

func (r *wireReader) expect(c byte) {
	r.space()
	if r.pos >= len(r.text) || r.text[r.pos] != c {
		r.fail("expected " + string(c))
	}
	r.pos++
}

func (r *wireReader) value() any {
	r.space()
	if r.pos >= len(r.text) {
		r.fail("unexpected end")
	}
	switch c := r.text[r.pos]; {
	case c == '{':
		return r.object()
	case c == '[':
		r.pos++
		out := []any{}
		r.space()
		if r.pos < len(r.text) && r.text[r.pos] == ']' {
			r.pos++
			return out
		}
		for {
			item := r.value()
			if _, undefined := item.(wireUndefined); undefined {
				item = nil // JSON.stringify writes undefined in an array as null
			}
			out = append(out, item)
			r.space()
			if r.pos < len(r.text) && r.text[r.pos] == ',' {
				r.pos++
				continue
			}
			r.expect(']')
			return out
		}
	case c == '"':
		return r.str()
	case strings.HasPrefix(r.text[r.pos:], "true"):
		r.pos += 4
		return true
	case strings.HasPrefix(r.text[r.pos:], "false"):
		r.pos += 5
		return false
	case strings.HasPrefix(r.text[r.pos:], "null"):
		r.pos += 4
		return nil
	}
	start := r.pos
	for r.pos < len(r.text) && strings.IndexByte("+-0123456789.eE", r.text[r.pos]) >= 0 {
		r.pos++
	}
	number, err := strconv.ParseFloat(r.text[start:r.pos], 64)
	if start == r.pos || (err != nil && number == 0) {
		r.fail("bad number")
	}
	return number
}

func (r *wireReader) str() string {
	start := r.pos
	r.pos++
	for r.pos < len(r.text) && r.text[r.pos] != '"' {
		if r.text[r.pos] == '\\' {
			r.pos++
		}
		r.pos++
	}
	if r.pos >= len(r.text) {
		r.fail("unterminated string")
	}
	r.pos++
	var s string
	if err := json.Unmarshal([]byte(r.text[start:r.pos]), &s); err != nil {
		// A lone surrogate escape: keep the code point as U+FFFD, the character Go's strings hold for it.
		s = strings.ToValidUTF8(strings.ReplaceAll(r.text[start+1:r.pos-1], `\`, ""), string(utf8.RuneError))
	}
	return s
}

// wireUndefined is the member the engine marks undefined: JSON.stringify leaves it out of an object and writes null in an array.
type wireUndefined struct{}

func (r *wireReader) object() any {
	r.pos++
	r.space()
	object := &OrderedObject{values: map[string]any{}}
	if r.pos < len(r.text) && r.text[r.pos] == '}' {
		r.pos++
		return object
	}
	type member struct {
		key   string
		value any
	}
	var members []member
	for {
		r.space()
		if r.pos >= len(r.text) || r.text[r.pos] != '"' {
			r.fail("expected a key")
		}
		key := r.str()
		r.expect(':')
		members = append(members, member{key, r.value()})
		r.space()
		if r.pos < len(r.text) && r.text[r.pos] == ',' {
			r.pos++
			continue
		}
		r.expect('}')
		break
	}
	if members[0].key == wireEnvelope {
		return r.envelope(members[0].value, func(name string) (any, bool) {
			for _, m := range members[1:] {
				if m.key == name {
					return m.value, true
				}
			}
			return nil, false
		})
	}
	for _, m := range members {
		if _, undefined := m.value.(wireUndefined); undefined {
			continue
		}
		object.set(m.key, m.value)
	}
	return object
}

func (r *wireReader) envelope(kind any, field func(string) (any, bool)) any {
	switch kind {
	case "u":
		return wireUndefined{}
	case "n":
		digits, _ := field("v")
		text, _ := digits.(string)
		value, ok := new(big.Int).SetString(text, 10)
		if !ok {
			r.fail("bad bigint")
		}
		return BigInt{value}
	case "f":
		index, _ := field("i")
		i, _ := index.(float64)
		if int(i) >= len(r.refs) {
			r.fail("unknown refinement")
		}
		return r.refs[int(i)]
	case "o":
		schema := &TSchema{object: &OrderedObject{values: map[string]any{}}, hidden: map[string]any{}}
		if h, ok := field("h"); ok {
			if hidden, _ := h.(*OrderedObject); hidden != nil {
				for _, key := range hidden.keys {
					value := hidden.values[key]
					if list, isList := value.([]any); isList && key == "~refine" {
						refinements := make([]Refinement, 0, len(list))
						for _, item := range list {
							if refinement, isRefinement := item.(Refinement); isRefinement {
								refinements = append(refinements, refinement)
							}
						}
						value = refinements
					}
					schema.hidden[key] = value
				}
			}
		}
		if e, ok := field("e"); ok {
			if enumerable, _ := e.(*OrderedObject); enumerable != nil {
				schema.object = enumerable
			}
		}
		return schema
	}
	r.fail("unknown envelope")
	return nil
}
