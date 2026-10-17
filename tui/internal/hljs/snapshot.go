package hljs

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A grammar snapshot is the JSON that automation/gen/generate-highlight-grammars.mjs writes: the grammar objects a set of languages reaches, the order the languages register in and, in the eager snapshot, the text tables. A property value is a JSON string, number, boolean or null, or one of five single-key objects: {"ref":n} names grammar object n, {"undef":true} is undefined, {"re":source} is a RegExp, {"arr":[...]} is an array and {"fn":name} is a function.
type snapshot struct {
	FirstNode     int
	Nodes         []snapshotNode
	Registrations []snapshotRegistration
	SystemSymbols []string
	Canonicalize  [][2]int
	LowerCase     []lowerCaseMapping
}

type snapshotNode struct {
	Kind   string
	Props  []snapshotProp
	Frozen bool
}

type snapshotProp struct {
	key   string
	value snapshotValue
}

type snapshotRegistration struct {
	name string
	node int
}

type lowerCaseMapping struct {
	unit rune
	text string
}

type snapshotKind uint8

const (
	valueNull snapshotKind = iota
	valueFalse
	valueTrue
	valueNumber
	valueString
	valueRef
	valueUndefined
	valueRegExp
	valueArray
	valueFunction
)

// snapshotValue is one property value. text holds a string, a RegExp source or a function name; number holds a number or a reference; list holds an array's items.
type snapshotValue struct {
	kind   snapshotKind
	text   string
	number float64
	list   []snapshotValue
}

// snapshotSections selects what decodeSnapshot builds; the other sections are scanned and skipped.
type snapshotSections uint8

const (
	snapshotGrammars snapshotSections = 1 << iota
	snapshotTables
)

// decodeSnapshot decompresses and parses a snapshot. The snapshot is generated and embedded, so malformed input is a build defect and panics. Strings decode as encoding/json decodes them.
func decodeSnapshot(data []byte, sections snapshotSections) *snapshot {
	raw, err := gunzip(data)
	if err != nil {
		panic(fmt.Sprintf("hljs: grammar snapshot: %v", err))
	}
	d := snapshotDecoder{data: raw, keys: map[string]string{}}
	s := &snapshot{}
	d.object(func(key []byte) {
		switch {
		case string(key) == "firstNode" && sections&snapshotGrammars != 0:
			s.FirstNode = int(d.number())
		case string(key) == "nodes" && sections&snapshotGrammars != 0:
			d.array(func() { s.Nodes = append(s.Nodes, d.node()) })
		case string(key) == "registrations" && sections&snapshotGrammars != 0:
			d.array(func() {
				var registration snapshotRegistration
				d.expect('[')
				registration.name = d.string()
				d.expect(',')
				registration.node = int(d.number())
				d.expect(']')
				s.Registrations = append(s.Registrations, registration)
			})
		case string(key) == "systemSymbols" && sections&snapshotTables != 0:
			d.array(func() { s.SystemSymbols = append(s.SystemSymbols, d.string()) })
		case string(key) == "canonicalize" && sections&snapshotTables != 0:
			d.array(func() {
				var pair [2]int
				d.expect('[')
				pair[0] = int(d.number())
				d.expect(',')
				pair[1] = int(d.number())
				d.expect(']')
				s.Canonicalize = append(s.Canonicalize, pair)
			})
		case string(key) == "lowerCase" && sections&snapshotTables != 0:
			d.array(func() {
				var mapping lowerCaseMapping
				d.expect('[')
				mapping.unit = rune(d.number())
				d.expect(',')
				mapping.text = d.string()
				d.expect(']')
				s.LowerCase = append(s.LowerCase, mapping)
			})
		default:
			d.skip()
		}
	})
	d.space()
	if d.pos != len(d.data) {
		d.fail("data after the snapshot")
	}
	return s
}

// gunzip reads the whole stream into one buffer sized from the gzip trailer, then verifies the checksum at end of stream.
func gunzip(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if len(data) < 4 {
		return nil, io.ErrUnexpectedEOF
	}
	raw := make([]byte, binary.LittleEndian.Uint32(data[len(data)-4:]))
	if _, err := io.ReadFull(reader, raw); err != nil {
		return nil, err
	}
	var extra [1]byte
	count, err := reader.Read(extra[:])
	switch {
	case count != 0:
		return nil, fmt.Errorf("more data than the trailer's size %d", len(raw))
	case errors.Is(err, io.EOF):
		return raw, nil
	case err == nil:
		return nil, fmt.Errorf("no end of stream after the trailer's size %d", len(raw))
	}
	return nil, err
}

type snapshotDecoder struct {
	data   []byte
	pos    int
	keys   map[string]string
	values []snapshotValue
	props  []snapshotProp
	// kept holds the props of finished nodes and the items of finished arrays; each chunk is shared by many of them.
	keptProps  []snapshotProp
	keptValues []snapshotValue
	// unescaped holds the last string decodeSimpleEscapes decoded.
	unescaped []byte
	// text is the chunk decoded strings are cut from. A strings.Builder never changes bytes it has written, so a string cut from it stays valid as the chunk fills.
	text strings.Builder
}

// snapshotTextChunk is the size in bytes of one chunk decoded strings are cut from.
const snapshotTextChunk = 16 << 10

// snapshotChunk is the number of props or array items one kept chunk holds.
const snapshotChunk = 1024

// keep copies items into the current chunk of arena, starting a chunk when it has no room, and returns the copy with its capacity clipped so an append to it cannot reach a neighbor.
func keep[T any](arena *[]T, items []T) []T {
	if cap(*arena)-len(*arena) < len(items) {
		*arena = make([]T, 0, max(snapshotChunk, len(items)))
	}
	start := len(*arena)
	*arena = append(*arena, items...)
	return (*arena)[start:len(*arena):len(*arena)]
}

// decodeSimpleEscapes decodes a string body whose escapes are all single-character escapes, as encoding/json does, into d.unescaped. It reports false for a \u escape or a malformed escape, which the caller leaves to encoding/json. The body must be valid UTF-8; the escapes it decodes are ASCII, so the result is valid UTF-8 too.
func (d *snapshotDecoder) decodeSimpleEscapes(body []byte) ([]byte, bool) {
	out := d.unescaped[:0]
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' {
			out = append(out, c)
			continue
		}
		i++
		if i == len(body) {
			return nil, false
		}
		switch body[i] {
		case '"', '\\', '/':
			out = append(out, body[i])
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		default:
			return nil, false
		}
	}
	d.unescaped = out
	return out, true
}

func (d *snapshotDecoder) fail(problem string) {
	panic(fmt.Sprintf("hljs: grammar snapshot: %s at offset %d", problem, d.pos))
}

func (d *snapshotDecoder) space() {
	for d.pos < len(d.data) {
		switch d.data[d.pos] {
		case ' ', '\t', '\n', '\r':
			d.pos++
		default:
			return
		}
	}
}

func (d *snapshotDecoder) peek() byte {
	d.space()
	if d.pos == len(d.data) {
		d.fail("unexpected end")
	}
	return d.data[d.pos]
}

func (d *snapshotDecoder) expect(c byte) {
	if d.peek() != c {
		d.fail("expected " + strconv.QuoteRune(rune(c)))
	}
	d.pos++
}

// object calls member with each key, positioned at its value; member must consume the value.
func (d *snapshotDecoder) object(member func(key []byte)) {
	d.expect('{')
	if d.peek() == '}' {
		d.pos++
		return
	}
	for {
		key := d.stringBytes()
		d.expect(':')
		member(key)
		if d.peek() == ',' {
			d.pos++
			continue
		}
		d.expect('}')
		return
	}
}

// array calls item at each item; item must consume it.
func (d *snapshotDecoder) array(item func()) {
	d.expect('[')
	if d.peek() == ']' {
		d.pos++
		return
	}
	for {
		item()
		if d.peek() == ',' {
			d.pos++
			continue
		}
		d.expect(']')
		return
	}
}

// stringBytes returns a string's decoded bytes. They alias the input or a buffer the next string reuses, so the caller copies them before keeping them.
func (d *snapshotDecoder) stringBytes() []byte {
	d.expect('"')
	start := d.pos
	plain := true
	for {
		if d.pos == len(d.data) {
			d.fail("unterminated string")
		}
		c := d.data[d.pos]
		switch {
		case c == '"':
			d.pos++
			span := d.data[start : d.pos-1]
			if utf8.Valid(span) {
				if plain {
					return span
				}
				if decoded, ok := d.decodeSimpleEscapes(span); ok {
					return decoded
				}
			}
			var decoded string
			if err := json.Unmarshal(d.data[start-1:d.pos], &decoded); err != nil {
				d.fail(err.Error())
			}
			return []byte(decoded)
		case c == '\\':
			plain = false
			d.pos += 2
		case c < 0x20:
			d.fail("control character in string")
		default:
			d.pos++
		}
	}
}

func (d *snapshotDecoder) string() string {
	return d.cut(d.stringBytes())
}

// cut copies span into the current text chunk, starting a new chunk when it does not fit, and returns the copy.
func (d *snapshotDecoder) cut(span []byte) string {
	if len(span) == 0 {
		return ""
	}
	if d.text.Cap()-d.text.Len() < len(span) {
		d.text = strings.Builder{}
		d.text.Grow(max(snapshotTextChunk, len(span)))
	}
	start := d.text.Len()
	d.text.Write(span)
	return d.text.String()[start:]
}

// intern returns a string, shared with every equal string interned before it. Grammars repeat their keys, class names and keywords.
func (d *snapshotDecoder) intern() string {
	span := d.stringBytes()
	if key, ok := d.keys[string(span)]; ok {
		return key
	}
	key := d.cut(span)
	d.keys[key] = key
	return key
}

func (d *snapshotDecoder) number() float64 {
	d.space()
	start := d.pos
	for d.pos < len(d.data) {
		c := d.data[d.pos]
		if (c < '0' || c > '9') && c != '-' && c != '+' && c != '.' && c != 'e' && c != 'E' {
			break
		}
		d.pos++
	}
	span := d.data[start:d.pos]
	if len(span) > 0 && len(span) < 16 {
		integer, digits := 0, span
		if span[0] == '-' {
			digits = span[1:]
		}
		ok := len(digits) > 0 && (digits[0] != '0' || len(digits) == 1)
		for _, c := range digits {
			if c < '0' || c > '9' {
				ok = false
				break
			}
			integer = integer*10 + int(c-'0')
		}
		if ok {
			if span[0] == '-' {
				return -float64(integer)
			}
			return float64(integer)
		}
	}
	var value float64
	if err := json.Unmarshal(span, &value); err != nil {
		d.fail("invalid number")
	}
	return value
}

func (d *snapshotDecoder) literal(text string) {
	if !bytes.HasPrefix(d.data[d.pos:], []byte(text)) {
		d.fail("invalid literal")
	}
	d.pos += len(text)
}

func (d *snapshotDecoder) node() snapshotNode {
	var node snapshotNode
	start := len(d.props)
	d.object(func(key []byte) {
		switch string(key) {
		case "k":
			node.Kind = d.intern()
		case "frozen":
			node.Frozen = d.boolean()
		case "p":
			d.array(func() {
				d.expect('[')
				key := d.intern()
				d.expect(',')
				value := d.value()
				d.expect(']')
				d.props = append(d.props, snapshotProp{key: key, value: value})
			})
		default:
			d.skip()
		}
	})
	if len(d.props) > start {
		node.Props = keep(&d.keptProps, d.props[start:])
	}
	d.props = d.props[:start]
	return node
}

func (d *snapshotDecoder) boolean() bool {
	switch d.peek() {
	case 't':
		d.literal("true")
		return true
	case 'f':
		d.literal("false")
		return false
	}
	d.fail("expected a boolean")
	return false
}

func (d *snapshotDecoder) value() snapshotValue {
	switch c := d.peek(); {
	case c == '"':
		return snapshotValue{kind: valueString, text: d.intern()}
	case c == 't' || c == 'f':
		if d.boolean() {
			return snapshotValue{kind: valueTrue}
		}
		return snapshotValue{kind: valueFalse}
	case c == 'n':
		d.literal("null")
		return snapshotValue{kind: valueNull}
	case c == '-' || c >= '0' && c <= '9':
		return snapshotValue{kind: valueNumber, number: d.number()}
	case c == '{':
		var value snapshotValue
		members := 0
		d.object(func(key []byte) {
			members++
			switch string(key) {
			case "ref":
				value = snapshotValue{kind: valueRef, number: d.number()}
			case "undef":
				if !d.boolean() {
					d.fail("undef is false")
				}
				value = snapshotValue{kind: valueUndefined}
			case "re":
				value = snapshotValue{kind: valueRegExp, text: d.string()}
			case "fn":
				value = snapshotValue{kind: valueFunction, text: d.intern()}
			case "arr":
				start := len(d.values)
				d.array(func() {
					item := d.value()
					d.values = append(d.values, item)
				})
				value = snapshotValue{kind: valueArray}
				if len(d.values) > start {
					value.list = keep(&d.keptValues, d.values[start:])
				}
				clear(d.values[start:])
				d.values = d.values[:start]
			default:
				d.fail("unknown object " + strconv.Quote(string(key)))
			}
		})
		if members != 1 {
			d.fail("an object value has " + strconv.Itoa(members) + " members")
		}
		return value
	}
	d.fail("unexpected value")
	return snapshotValue{}
}

// skip consumes one value without building it.
func (d *snapshotDecoder) skip() {
	switch d.peek() {
	case '"':
		d.stringBytes()
	case '{':
		d.object(func([]byte) { d.skip() })
	case '[':
		d.array(d.skip)
	case 't', 'f':
		d.boolean()
	case 'n':
		d.literal("null")
	default:
		d.number()
	}
}
