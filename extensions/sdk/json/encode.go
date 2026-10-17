// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package json implements encoding/json's Go mapping with JavaScript string semantics.
// Lone UTF-16 units use WTF-8 in Go strings and standard surrogate escapes on the wire.
// The mapping between JSON and Go values is described in the documentation for
// the Marshal and Unmarshal functions.
//
// See "JSON and Go" for an introduction to this package:
// https://golang.org/doc/articles/json_and_go.html
//
// # Security Considerations
//
// The JSON standard (RFC 7159) is lax in its definition of a number of parser
// behaviors. As such, many JSON parsers behave differently in various
// scenarios. These differences in parsers mean that systems that use multiple
// independent JSON parser implementations may parse the same JSON object in
// differing ways.
//
// Systems that rely on a JSON object being parsed consistently for security
// purposes should be careful to understand the behaviors of this parser, as
// well as how these behaviors may cause interoperability issues with other
// parser implementations.
//
// Due to the Go Backwards Compatibility promise (https://go.dev/doc/go1compat)
// there are a number of behaviors this package exhibits that may cause
// interoperability issues, but cannot be changed. In particular the following
// parsing behaviors may cause issues:
//
//   - If a JSON object contains duplicate keys, keys are processed in the order
//     they are observed, meaning later values will replace or be merged into
//     prior values, depending on the field type (in particular maps and structs
//     will have values merged, while other types have values replaced).
//   - When parsing a JSON object into a Go struct, keys are considered in a
//     case-insensitive fashion.
//   - When parsing a JSON object into a Go struct, unknown keys in the JSON
//     object are ignored (unless a [Decoder] is used and
//     [Decoder.DisallowUnknownFields] has been called).
//   - Invalid UTF-8 bytes in JSON strings are replaced by the Unicode
//     replacement character.
//   - Large JSON number integers will lose precision when unmarshaled into
//     floating-point types.
package json

import (
	"bytes"
	"cmp"
	"encoding"
	"encoding/base64"
	"fmt"
	"math"
	"math/bits"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Marshal returns the JSON encoding of v.
//
// Marshal traverses the value v recursively.
// If an encountered value implements [Marshaler]
// and is not a nil pointer, Marshal calls [Marshaler.MarshalJSON]
// to produce JSON. If no [Marshaler.MarshalJSON] method is present but the
// value implements [encoding.TextMarshaler] instead, Marshal calls
// [encoding.TextMarshaler.MarshalText] and encodes the result as a JSON string.
// The nil pointer exception is not strictly necessary
// but mimics a similar, necessary exception in the behavior of
// [Unmarshaler.UnmarshalJSON].
//
// Otherwise, Marshal uses the following type-dependent default encodings:
//
// Boolean values encode as JSON booleans.
//
// Floating point, integer, and [Number] values encode as JSON numbers.
// NaN and +/-Inf values will return an [UnsupportedValueError].
//
// String values encode as JSON strings. Lone UTF-16 units represented in WTF-8
// encode as Unicode escapes; other invalid UTF-8 bytes encode as U+FFFD.
// So that the JSON will be safe to embed inside HTML <script> tags,
// the string is encoded using [HTMLEscape],
// which replaces "<", ">", "&", U+2028, and U+2029 are escaped
// to "\u003c","\u003e", "\u0026", "\u2028", and "\u2029".
// This replacement can be disabled when using an [Encoder],
// by calling [Encoder.SetEscapeHTML](false).
//
// Array and slice values encode as JSON arrays, except that
// []byte encodes as a base64-encoded string, and a nil slice
// encodes as the null JSON value.
//
// Struct values encode as JSON objects.
// Each exported struct field becomes a member of the object, using the
// field name as the object key, unless the field is omitted for one of the
// reasons given below.
//
// The encoding of each struct field can be customized by the format string
// stored under the "json" key in the struct field's tag.
// The format string gives the name of the field, possibly followed by a
// comma-separated list of options. The name may be empty in order to
// specify options without overriding the default field name.
//
// The "omitempty" option specifies that the field should be omitted
// from the encoding if the field has an empty value, defined as
// false, 0, a nil pointer, a nil interface value, and any array,
// slice, map, or string of length zero.
//
// As a special case, if the field tag is "-", the field is always omitted.
// Note that a field with name "-" can still be generated using the tag "-,".
//
// Examples of struct field tags and their meanings:
//
//	// Field appears in JSON as key "myName".
//	Field int `json:"myName"`
//
//	// Field appears in JSON as key "myName" and
//	// the field is omitted from the object if its value is empty,
//	// as defined above.
//	Field int `json:"myName,omitempty"`
//
//	// Field appears in JSON as key "Field" (the default), but
//	// the field is skipped if empty.
//	// Note the leading comma.
//	Field int `json:",omitempty"`
//
//	// Field is ignored by this package.
//	Field int `json:"-"`
//
//	// Field appears in JSON as key "-".
//	Field int `json:"-,"`
//
// The "omitzero" option specifies that the field should be omitted
// from the encoding if the field has a zero value, according to rules:
//
// 1) If the field type has an "IsZero() bool" method, that will be used to
// determine whether the value is zero.
//
// 2) Otherwise, the value is zero if it is the zero value for its type.
//
// If both "omitempty" and "omitzero" are specified, the field will be omitted
// if the value is either empty or zero (or both).
//
// The "string" option signals that a field is stored as JSON inside a
// JSON-encoded string. It applies only to fields of string, floating point,
// integer, or boolean types. This extra level of encoding is sometimes used
// when communicating with JavaScript programs:
//
//	Int64String int64 `json:",string"`
//
// The key name will be used if it's a non-empty string consisting of
// only Unicode letters, digits, and ASCII punctuation except quotation
// marks, backslash, and comma.
//
// Embedded struct fields are usually marshaled as if their inner exported fields
// were fields in the outer struct, subject to the usual Go visibility rules amended
// as described in the next paragraph.
// An anonymous struct field with a name given in its JSON tag is treated as
// having that name, rather than being anonymous.
// An anonymous struct field of interface type is treated the same as having
// that type as its name, rather than being anonymous.
//
// The Go visibility rules for struct fields are amended for JSON when
// deciding which field to marshal or unmarshal. If there are
// multiple fields at the same level, and that level is the least
// nested (and would therefore be the nesting level selected by the
// usual Go rules), the following extra rules apply:
//
// 1) Of those fields, if any are JSON-tagged, only tagged fields are considered,
// even if there are multiple untagged fields that would otherwise conflict.
//
// 2) If there is exactly one field (tagged or not according to the first rule), that is selected.
//
// 3) Otherwise there are multiple fields, and all are ignored; no error occurs.
//
// Handling of anonymous struct fields is new in Go 1.1.
// Prior to Go 1.1, anonymous struct fields were ignored. To force ignoring of
// an anonymous struct field in both current and earlier versions, give the field
// a JSON tag of "-".
//
// Map values encode as JSON objects. The map's key type must either be a
// string, an integer type, or implement [encoding.TextMarshaler]. The map keys
// are sorted and used as JSON object keys by applying the following rules,
// subject to the UTF-8 coercion described for string values above:
//   - keys of any string type are used directly
//   - keys that implement [encoding.TextMarshaler] are marshaled
//   - integer keys are converted to strings
//
// Pointer values encode as the value pointed to.
// A nil pointer encodes as the null JSON value.
//
// Interface values encode as the value contained in the interface.
// A nil interface value encodes as the null JSON value.
//
// Channel, complex, and function values cannot be encoded in JSON.
// Attempting to encode such a value causes Marshal to return
// an [UnsupportedTypeError].
//
// JSON cannot represent cyclic data structures and Marshal does not
// handle them. Passing cyclic structures to Marshal will result in
// an error.
func Marshal(v any) ([]byte, error) {
	e := newEncodeState()
	defer putEncodeState(e)

	err := e.marshal(v, encOpts{escapeHTML: true})
	if err != nil {
		return nil, err
	}
	buf := append([]byte(nil), e.Bytes()...)

	return buf, nil
}

// MarshalJS is like [Marshal] but writes numbers as JavaScript's JSON.stringify does: NaN and the infinities are null rather than an
// [UnsupportedValueError], and negative zero is 0. The extension wire carries what JSON.stringify would write.
func MarshalJS(v any) ([]byte, error) {
	e := newEncodeState()
	defer putEncodeState(e)

	err := e.marshal(v, encOpts{escapeHTML: true, jsNumbers: true})
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), e.Bytes()...), nil
}

// pig additive (lw-json): the JSON.stringify mode.

// stringifyOpts is the JSON.stringify mode: numbers as [MarshalJS] writes them, strings with only the escapes JSON.stringify writes (no HTML
// escaping, U+2028 and U+2029 raw), and [Marshaler] output rewritten to match (see [MarshalStringify]).
var stringifyOpts = encOpts{jsNumbers: true, jsStrings: true}

// MarshalStringify returns the JSON.stringify text of v. It is [MarshalJS] without HTML escaping, with U+2028 and U+2029 raw, and with the
// U+FFFD that replaces each maximal ill-formed UTF-8 subpart written raw (as a JavaScript string decoded from those bytes holds it). It
// rewrites what a nested [Marshaler] returns into the same form while compacting it: the escapes \u003c, \u003e (either hex case), \u0026,
// \u2028, \u2029 and \ufffd (any hex case) become the characters, and a negative number equal to zero (a json.Number "-0" too) becomes 0.
// Other escapes are kept. A [StringifyAppender] appends its text directly, without that pass.
func MarshalStringify(v any) ([]byte, error) {
	e := newEncodeState()
	defer putEncodeState(e)
	if err := e.marshal(v, stringifyOpts); err != nil {
		return nil, err
	}
	// A buffer too large to pool becomes the result when little of it is spare; otherwise the result is an exact copy.
	if size := len(e.buf); cap(e.buf) > maxPooledEncodeBuffer && cap(e.buf)-size <= size/4 {
		out := e.buf
		e.buf = nil
		return out, nil
	}
	return append([]byte(nil), e.buf...), nil
}

// AppendStringify appends [MarshalStringify] of v to dst, encoding into dst itself. On an error it returns dst with its original length.
func AppendStringify(dst []byte, v any) ([]byte, error) {
	e := newEncodeState()
	pooled := e.buf
	e.buf = dst
	err := e.marshal(v, stringifyOpts)
	out := e.buf
	e.buf = pooled
	putEncodeState(e)
	if err != nil {
		return dst[:len(dst):len(dst)], err
	}
	return out, nil
}

// AppendStringifyString appends the JSON.stringify text of the Go string s to dst: what [AppendStringify] writes for s without the reflection walk. pig additive (lw-json).
func AppendStringifyString(dst []byte, s string) []byte {
	return appendString(dst, s, stringifyOpts.stringEscapes())
}

// A StringifyAppender is a [Marshaler] that can also append its JSON.stringify text (what [MarshalStringify] of its MarshalJSON result
// writes: compact, JSON.stringify escaping and numbers) to dst. In the JSON.stringify mode the encoder splices that text without reading it
// again; every other mode calls MarshalJSON. Tests check each appended text against the compaction it skips.
type StringifyAppender interface {
	Marshaler
	AppendStringify(dst []byte) ([]byte, error)
}

// MarshalIndent is like [Marshal] but applies [Indent] to format the output.
// Each JSON element in the output will begin on a new line beginning with prefix
// followed by one or more copies of indent according to the indentation nesting.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	b, err := Marshal(v)
	if err != nil {
		return nil, err
	}
	b2 := make([]byte, 0, indentGrowthFactor*len(b))
	b2, err = appendIndent(b2, b, prefix, indent)
	if err != nil {
		return nil, err
	}
	return b2, nil
}

// Marshaler is the interface implemented by types that
// can marshal themselves into valid JSON.
type Marshaler interface {
	MarshalJSON() ([]byte, error)
}

// An UnsupportedTypeError is returned by [Marshal] when attempting
// to encode an unsupported value type.
type UnsupportedTypeError struct {
	Type reflect.Type
}

func (e *UnsupportedTypeError) Error() string {
	return "json: unsupported type: " + e.Type.String()
}

// An UnsupportedValueError is returned by [Marshal] when attempting
// to encode an unsupported value.
type UnsupportedValueError struct {
	Value reflect.Value
	Str   string
}

func (e *UnsupportedValueError) Error() string {
	return "json: unsupported value: " + e.Str
}

// Before Go 1.2, an InvalidUTF8Error was returned by [Marshal] when
// attempting to encode a string value with invalid UTF-8 sequences.
// As of Go 1.2, [Marshal] instead coerces the string to valid UTF-8 by
// replacing invalid bytes with the Unicode replacement rune U+FFFD.
//
// Deprecated: No longer used; kept for compatibility.
type InvalidUTF8Error struct {
	S string // the whole string value that caused the error
}

func (e *InvalidUTF8Error) Error() string {
	return "json: invalid UTF-8 in string: " + strconv.Quote(e.S)
}

// A MarshalerError represents an error from calling a
// [Marshaler.MarshalJSON] or [encoding.TextMarshaler.MarshalText] method.
type MarshalerError struct {
	Type       reflect.Type
	Err        error
	sourceFunc string
}

func (e *MarshalerError) Error() string {
	srcFunc := e.sourceFunc
	if srcFunc == "" {
		srcFunc = "MarshalJSON"
	}
	return "json: error calling " + srcFunc +
		" for type " + e.Type.String() +
		": " + e.Err.Error()
}

// Unwrap returns the underlying error.
func (e *MarshalerError) Unwrap() error { return e.Err }

const hex = "0123456789abcdef"

// An encodeState encodes JSON into buf.
// pig: a plain slice rather than an embedded bytes.Buffer, so a [StringifyAppender] and [AppendStringify] append to the output itself.
type encodeState struct {
	buf []byte // accumulated output

	// Keep track of what pointers we've seen in the current recursive call
	// path, to avoid cycles that could lead to a stack overflow. Only do
	// the relatively expensive map operations if ptrLevel is larger than
	// startDetectingCyclesAfter, so that we skip the work if we're within a
	// reasonable amount of nested pointers deep.
	ptrLevel uint
	ptrSeen  map[any]struct{}
}

// Bytes returns the output.
func (e *encodeState) Bytes() []byte { return e.buf }

// AvailableBuffer returns an empty slice at the end of the output, for an append whose result is passed to Write.
func (e *encodeState) AvailableBuffer() []byte { return e.buf[len(e.buf):] }

// Write appends p. A p appended to AvailableBuffer without reallocating is already in place and only extends the output.
func (e *encodeState) Write(p []byte) {
	if n := len(e.buf); len(p) > 0 && cap(e.buf) > n && &e.buf[:n+1][n] == &p[0] {
		e.buf = e.buf[:n+len(p)]
		return
	}
	e.buf = append(e.buf, p...)
}

func (e *encodeState) WriteString(s string) { e.buf = append(e.buf, s...) }

// WriteByte appends c. It returns nil, with io.ByteWriter's signature.
func (e *encodeState) WriteByte(c byte) error {
	e.buf = append(e.buf, c)
	return nil
}

// Grow makes room for n more bytes, doubling the capacity rather than growing by append's smaller steps.
func (e *encodeState) Grow(n int) {
	if cap(e.buf)-len(e.buf) < n {
		grown := make([]byte, len(e.buf), 2*cap(e.buf)+n)
		copy(grown, e.buf)
		e.buf = grown
	}
}

const startDetectingCyclesAfter = 1000

var encodeStatePool sync.Pool

// maxPooledEncodeBuffer bounds the buffer an encode state keeps in the pool (golang/go#23199): one large value must not pin its buffer.
const maxPooledEncodeBuffer = 64 << 10

func newEncodeState() *encodeState {
	if v := encodeStatePool.Get(); v != nil {
		e := v.(*encodeState)
		e.buf = e.buf[:0]
		if len(e.ptrSeen) > 0 {
			panic("ptrEncoder.encode should have emptied ptrSeen via defers")
		}
		e.ptrLevel = 0
		return e
	}
	return &encodeState{ptrSeen: make(map[any]struct{})}
}

// putEncodeState returns e to the pool without a buffer larger than maxPooledEncodeBuffer.
func putEncodeState(e *encodeState) {
	if cap(e.buf) > maxPooledEncodeBuffer {
		e.buf = nil
	}
	encodeStatePool.Put(e)
}

// jsonError is an error wrapper type for internal use only.
// Panics with errors are wrapped in jsonError so that the top-level recover
// can distinguish intentional panics from this package.
type jsonError struct{ error }

func (e *encodeState) marshal(v any, opts encOpts) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if je, ok := r.(jsonError); ok {
				err = je.error
			} else {
				panic(r)
			}
		}
	}()
	e.reflectValue(reflect.ValueOf(v), opts)
	return nil
}

// error aborts the encoding by panicking with err wrapped in jsonError.
func (e *encodeState) error(err error) {
	panic(jsonError{err})
}

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}

func (e *encodeState) reflectValue(v reflect.Value, opts encOpts) {
	valueEncoder(v)(e, v, opts)
}

type encOpts struct {
	// quoted causes primitive fields to be encoded inside JSON strings.
	quoted bool
	// escapeHTML causes '<', '>', and '&' to be escaped in JSON strings.
	escapeHTML bool
	// jsNumbers makes numbers print as JavaScript's JSON.stringify does: a non-finite number is null and negative zero is 0.
	jsNumbers bool
	// jsStrings is the JSON.stringify mode of MarshalStringify: U+2028 and U+2029 stay raw, Marshaler output is rewritten by
	// appendCompactJS, and a StringifyAppender appends its own text. pig additive (lw-json).
	jsStrings bool
}

// stringEscapes is the set of optional escapes appendString writes for opts.
func (opts encOpts) stringEscapes() stringEscapes {
	var escapes stringEscapes
	if opts.escapeHTML {
		escapes |= escapeHTMLChars
	}
	if !opts.jsStrings {
		escapes |= escapeLineSeparators | escapeReplacement
	}
	return escapes
}

type encoderFunc func(e *encodeState, v reflect.Value, opts encOpts)

var encoderCache sync.Map // map[reflect.Type]encoderFunc

func valueEncoder(v reflect.Value) encoderFunc {
	if !v.IsValid() {
		return invalidValueEncoder
	}
	return typeEncoder(v.Type())
}

func typeEncoder(t reflect.Type) encoderFunc {
	if fi, ok := encoderCache.Load(t); ok {
		return fi.(encoderFunc)
	}

	// To deal with recursive types, populate the map with an
	// indirect func before we build it. If the type is recursive,
	// the second lookup for the type will return the indirect func.
	//
	// This indirect func is only used for recursive types,
	// and briefly during racing calls to typeEncoder.
	indirect := sync.OnceValue(func() encoderFunc {
		return newTypeEncoder(t, true)
	})
	fi, loaded := encoderCache.LoadOrStore(t, encoderFunc(func(e *encodeState, v reflect.Value, opts encOpts) {
		indirect()(e, v, opts)
	}))
	if loaded {
		return fi.(encoderFunc)
	}

	f := indirect()
	encoderCache.Store(t, f)
	return f
}

var (
	marshalerType     = reflect.TypeFor[Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

// newTypeEncoder constructs an encoderFunc for a type.
// The returned encoder only checks CanAddr when allowAddr is true.
func newTypeEncoder(t reflect.Type, allowAddr bool) encoderFunc {
	// If we have a non-pointer value whose type implements
	// Marshaler with a value receiver, then we're better off taking
	// the address of the value - otherwise we end up with an
	// allocation as we cast the value to an interface.
	if t.Kind() != reflect.Pointer && allowAddr && reflect.PointerTo(t).Implements(marshalerType) {
		return newCondAddrEncoder(addrMarshalerEncoder, newTypeEncoder(t, false))
	}
	if t.Implements(marshalerType) {
		return marshalerEncoder
	}
	if t.Kind() != reflect.Pointer && allowAddr && reflect.PointerTo(t).Implements(textMarshalerType) {
		return newCondAddrEncoder(addrTextMarshalerEncoder, newTypeEncoder(t, false))
	}
	if t.Implements(textMarshalerType) {
		return textMarshalerEncoder
	}

	switch t.Kind() {
	case reflect.Bool:
		return boolEncoder
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return intEncoder
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return uintEncoder
	case reflect.Float32:
		return float32Encoder
	case reflect.Float64:
		return float64Encoder
	case reflect.String:
		return stringEncoder
	case reflect.Interface:
		return interfaceEncoder
	case reflect.Struct:
		return newStructEncoder(t)
	case reflect.Map:
		return newMapEncoder(t)
	case reflect.Slice:
		return newSliceEncoder(t)
	case reflect.Array:
		return newArrayEncoder(t)
	case reflect.Pointer:
		return newPtrEncoder(t)
	default:
		return unsupportedTypeEncoder
	}
}

func invalidValueEncoder(e *encodeState, v reflect.Value, _ encOpts) {
	e.WriteString("null")
}

func marshalerEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	if v.Kind() == reflect.Pointer && v.IsNil() {
		e.WriteString("null")
		return
	}
	m, ok := reflect.TypeAssert[Marshaler](v)
	if !ok {
		e.WriteString("null")
		return
	}
	e.marshaler(m, v.Type(), opts)
}

func addrMarshalerEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	va := v.Addr()
	if va.IsNil() {
		e.WriteString("null")
		return
	}
	m, _ := reflect.TypeAssert[Marshaler](va)
	e.marshaler(m, v.Type(), opts)
}

// marshaler writes the output of m, a value of type t. In the JSON.stringify mode a StringifyAppender appends its text to the output; any
// other Marshaler's output is compacted (and in that mode rewritten by appendCompactJS) into it.
func (e *encodeState) marshaler(m Marshaler, t reflect.Type, opts encOpts) {
	if opts.jsStrings {
		if appender, ok := m.(StringifyAppender); ok {
			start := len(e.buf)
			out, err := appender.AppendStringify(e.buf)
			if err != nil {
				// The same error MarshalJSON reports: AppendStringify is its JSON.stringify form.
				e.error(&MarshalerError{t, err, "MarshalJSON"})
			}
			e.buf = out
			if verifyTrusted {
				verifyAppended(m, t, out[start:])
			}
			return
		}
	}
	b, err := m.MarshalJSON()
	if err == nil {
		e.Grow(len(b))
		if opts.jsStrings {
			e.buf, err = appendCompactJS(e.buf, b)
		} else {
			e.buf, err = appendCompact(e.buf, b, opts.escapeHTML)
		}
	}
	if err != nil {
		e.error(&MarshalerError{t, err, "MarshalJSON"})
	}
}

// verifyAppended panics unless appended is what the encoder writes for MarshalJSON's output in the JSON.stringify mode.
func verifyAppended(m Marshaler, t reflect.Type, appended []byte) {
	b, err := m.MarshalJSON()
	if err != nil {
		panic(fmt.Sprintf("json: %s.AppendStringify succeeded where MarshalJSON failed: %v", t, err))
	}
	want, err := appendCompactJS(nil, b)
	if err != nil || !bytes.Equal(want, appended) {
		panic(fmt.Sprintf("json: %s.AppendStringify wrote %q, MarshalJSON in the JSON.stringify mode %q (%v)", t, appended, want, err))
	}
}

func textMarshalerEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	if v.Kind() == reflect.Pointer && v.IsNil() {
		e.WriteString("null")
		return
	}
	m, ok := reflect.TypeAssert[encoding.TextMarshaler](v)
	if !ok {
		e.WriteString("null")
		return
	}
	b, err := m.MarshalText()
	if err != nil {
		e.error(&MarshalerError{v.Type(), err, "MarshalText"})
	}
	e.Write(appendString(e.AvailableBuffer(), b, opts.stringEscapes()))
}

func addrTextMarshalerEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	va := v.Addr()
	if va.IsNil() {
		e.WriteString("null")
		return
	}
	m, _ := reflect.TypeAssert[encoding.TextMarshaler](va)
	b, err := m.MarshalText()
	if err != nil {
		e.error(&MarshalerError{v.Type(), err, "MarshalText"})
	}
	e.Write(appendString(e.AvailableBuffer(), b, opts.stringEscapes()))
}

func boolEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	b := e.AvailableBuffer()
	b = mayAppendQuote(b, opts.quoted)
	b = strconv.AppendBool(b, v.Bool())
	b = mayAppendQuote(b, opts.quoted)
	e.Write(b)
}

func intEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	b := e.AvailableBuffer()
	b = mayAppendQuote(b, opts.quoted)
	b = strconv.AppendInt(b, v.Int(), 10)
	b = mayAppendQuote(b, opts.quoted)
	e.Write(b)
}

func uintEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	b := e.AvailableBuffer()
	b = mayAppendQuote(b, opts.quoted)
	b = strconv.AppendUint(b, v.Uint(), 10)
	b = mayAppendQuote(b, opts.quoted)
	e.Write(b)
}

type floatEncoder int // number of bits

func (bits floatEncoder) encode(e *encodeState, v reflect.Value, opts encOpts) {
	f := v.Float()
	if math.IsInf(f, 0) || math.IsNaN(f) {
		if opts.jsNumbers {
			e.WriteString("null")
			return
		}
		e.error(&UnsupportedValueError{v, strconv.FormatFloat(f, 'g', -1, int(bits))})
	}
	if opts.jsNumbers && f == 0 {
		f = 0 // JSON.stringify(-0) is "0"
	}

	// Convert as if by ES6 number to string conversion.
	// This matches most other JSON generators.
	// See golang.org/issue/6384 and golang.org/issue/14135.
	// Like fmt %g, but the exponent cutoffs are different
	// and exponents themselves are not padded to two digits.
	b := e.AvailableBuffer()
	b = mayAppendQuote(b, opts.quoted)
	abs := math.Abs(f)
	fmt := byte('f')
	// Note: Must use float32 comparisons for underlying float32 value to get precise cutoffs right.
	if abs != 0 {
		if bits == 64 && (abs < 1e-6 || abs >= 1e21) || bits == 32 && (float32(abs) < 1e-6 || float32(abs) >= 1e21) {
			fmt = 'e'
		}
	}
	b = strconv.AppendFloat(b, f, fmt, -1, int(bits))
	if fmt == 'e' {
		// clean up e-09 to e-9
		n := len(b)
		if n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}
	b = mayAppendQuote(b, opts.quoted)
	e.Write(b)
}

var (
	float32Encoder = (floatEncoder(32)).encode
	float64Encoder = (floatEncoder(64)).encode
)

func stringEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	if v.Type() == numberType {
		numStr := v.String()
		// In Go1.5 the empty string encodes to "0", while this is not a valid number literal
		// we keep compatibility so check validity after this.
		if numStr == "" {
			numStr = "0" // Number's zero-val
		}
		if !isValidNumber(numStr) {
			e.error(fmt.Errorf("json: invalid number literal %q", numStr))
		}
		if opts.jsStrings && !opts.quoted && numStr[0] == '-' {
			// JSON.stringify writes negative zero as 0, whatever digits spell it.
			if value, err := strconv.ParseFloat(numStr, 64); value == 0 && err == nil {
				numStr = "0"
			}
		}
		b := e.AvailableBuffer()
		b = mayAppendQuote(b, opts.quoted)
		b = append(b, numStr...)
		b = mayAppendQuote(b, opts.quoted)
		e.Write(b)
		return
	}
	if opts.quoted {
		// The inner text keeps U+2028, U+2029 and the U+FFFD of ill-formed UTF-8 escaped in every mode, so the outer quoting escapes its
		// backslash.
		b := appendString(nil, v.String(), opts.stringEscapes()|escapeLineSeparators|escapeReplacement)
		e.Write(appendString(e.AvailableBuffer(), b, escapeLineSeparators)) // no need to escape again since it is already escaped
	} else {
		s := v.String()
		// Room for the text and its quotes and a few escapes, so a long string does not grow the output by append's small steps. Output that
		// already holds the text and its quotes is not regrown for the escapes it may not have: a buffer sized for its content stays the one.
		if cap(e.buf)-len(e.buf) < len(s)+2 {
			e.Grow(len(s) + len(s)/8 + 2)
		}
		e.Write(appendString(e.AvailableBuffer(), s, opts.stringEscapes()))
	}
}

func isValidNumber(s string) bool {
	// This function implements the JSON numbers grammar.
	// See https://tools.ietf.org/html/rfc7159#section-6
	// and https://www.json.org/img/number.png

	if s == "" {
		return false
	}

	// Optional -
	if s[0] == '-' {
		s = s[1:]
		if s == "" {
			return false
		}
	}

	// Digits
	switch {
	default:
		return false

	case s[0] == '0':
		s = s[1:]

	case '1' <= s[0] && s[0] <= '9':
		s = s[1:]
		for len(s) > 0 && '0' <= s[0] && s[0] <= '9' {
			s = s[1:]
		}
	}

	// . followed by 1 or more digits.
	if len(s) >= 2 && s[0] == '.' && '0' <= s[1] && s[1] <= '9' {
		s = s[2:]
		for len(s) > 0 && '0' <= s[0] && s[0] <= '9' {
			s = s[1:]
		}
	}

	// e or E followed by an optional - or + and
	// 1 or more digits.
	if len(s) >= 2 && (s[0] == 'e' || s[0] == 'E') {
		s = s[1:]
		if s[0] == '+' || s[0] == '-' {
			s = s[1:]
			if s == "" {
				return false
			}
		}
		for len(s) > 0 && '0' <= s[0] && s[0] <= '9' {
			s = s[1:]
		}
	}

	// Make sure we are at the end.
	return s == ""
}

func interfaceEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	if v.IsNil() {
		e.WriteString("null")
		return
	}
	e.reflectValue(v.Elem(), opts)
}

func unsupportedTypeEncoder(e *encodeState, v reflect.Value, _ encOpts) {
	e.error(&UnsupportedTypeError{v.Type()})
}

type structEncoder struct {
	fields structFields
}

type structFields struct {
	list         []field
	byExactName  map[string]*field
	byFoldedName map[string]*field
}

func (se structEncoder) encode(e *encodeState, v reflect.Value, opts encOpts) {
	next := byte('{')
FieldLoop:
	for i := range se.fields.list {
		f := &se.fields.list[i]

		// Find the nested struct field by following f.index.
		fv := v
		for _, i := range f.index {
			if fv.Kind() == reflect.Pointer {
				if fv.IsNil() {
					continue FieldLoop
				}
				fv = fv.Elem()
			}
			fv = fv.Field(i)
		}

		if (f.omitEmpty && isEmptyValue(fv)) ||
			(f.omitZero && (f.isZero == nil && fv.IsZero() || (f.isZero != nil && f.isZero(fv)))) {
			continue
		}
		e.WriteByte(next)
		next = ','
		if opts.escapeHTML {
			e.WriteString(f.nameEscHTML)
		} else {
			e.WriteString(f.nameNonEsc)
		}
		opts.quoted = f.quoted
		f.encoder(e, fv, opts)
	}
	if next == '{' {
		e.WriteString("{}")
	} else {
		e.WriteByte('}')
	}
}

func newStructEncoder(t reflect.Type) encoderFunc {
	se := structEncoder{fields: cachedTypeFields(t)}
	return se.encode
}

type mapEncoder struct {
	elemEnc encoderFunc
}

// arrayIndexKey reports whether key is a canonical array index: an integer from 0 to 2^32-2 without leading zeros or a sign.
func arrayIndexKey(key string) (uint64, bool) {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return 0, false
	}
	var value uint64
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return 0, false
		}
		value = value*10 + uint64(key[i]-'0')
	}
	if value >= 1<<32-1 {
		return 0, false
	}
	return value, true
}

// compareJSKeys orders object keys as JavaScript enumerates them for a map without insertion order: array indexes ascending, then the rest in byte order.
func compareJSKeys(a, b string) int {
	x, xIndex := arrayIndexKey(a)
	y, yIndex := arrayIndexKey(b)
	switch {
	case xIndex && yIndex:
		return cmp.Compare(x, y)
	case xIndex:
		return -1
	case yIndex:
		return 1
	}
	return strings.Compare(a, b)
}

func (me mapEncoder) encode(e *encodeState, v reflect.Value, opts encOpts) {
	if v.IsNil() {
		e.WriteString("null")
		return
	}
	if e.ptrLevel++; e.ptrLevel > startDetectingCyclesAfter {
		// We're a large number of nested ptrEncoder.encode calls deep;
		// start checking if we've run into a pointer cycle.
		ptr := v.UnsafePointer()
		if _, ok := e.ptrSeen[ptr]; ok {
			e.error(&UnsupportedValueError{v, fmt.Sprintf("encountered a cycle via %s", v.Type())})
		}
		e.ptrSeen[ptr] = struct{}{}
		defer delete(e.ptrSeen, ptr)
	}
	e.WriteByte('{')

	// Extract and sort the keys.
	var (
		sv  = make([]reflectWithString, v.Len())
		mi  = v.MapRange()
		err error
	)
	for i := 0; mi.Next(); i++ {
		if sv[i].ks, err = resolveKeyName(mi.Key()); err != nil {
			e.error(fmt.Errorf("json: encoding error for type %q: %q", v.Type().String(), err.Error()))
		}
		sv[i].v = mi.Value()
	}
	if opts.jsNumbers {
		// JSON.stringify writes the array-index keys of an object first in ascending numeric order, then the other keys; a Go map has no insertion
		// order, so the other keys stay in byte order.
		slices.SortFunc(sv, func(i, j reflectWithString) int { return compareJSKeys(i.ks, j.ks) })
	} else {
		slices.SortFunc(sv, func(i, j reflectWithString) int {
			return strings.Compare(i.ks, j.ks)
		})
	}

	for i, kv := range sv {
		if i > 0 {
			e.WriteByte(',')
		}
		e.Write(appendString(e.AvailableBuffer(), kv.ks, opts.stringEscapes()))
		e.WriteByte(':')
		me.elemEnc(e, kv.v, opts)
	}
	e.WriteByte('}')
	e.ptrLevel--
}

func newMapEncoder(t reflect.Type) encoderFunc {
	switch t.Key().Kind() {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
	default:
		if !t.Key().Implements(textMarshalerType) {
			return unsupportedTypeEncoder
		}
	}
	me := mapEncoder{typeEncoder(t.Elem())}
	return me.encode
}

func encodeByteSlice(e *encodeState, v reflect.Value, _ encOpts) {
	if v.IsNil() {
		e.WriteString("null")
		return
	}

	s := v.Bytes()
	b := e.AvailableBuffer()
	b = append(b, '"')
	b = base64.StdEncoding.AppendEncode(b, s)
	b = append(b, '"')
	e.Write(b)
}

// sliceEncoder just wraps an arrayEncoder, checking to make sure the value isn't nil.
type sliceEncoder struct {
	arrayEnc encoderFunc
}

func (se sliceEncoder) encode(e *encodeState, v reflect.Value, opts encOpts) {
	if v.IsNil() {
		e.WriteString("null")
		return
	}
	if e.ptrLevel++; e.ptrLevel > startDetectingCyclesAfter {
		// We're a large number of nested ptrEncoder.encode calls deep;
		// start checking if we've run into a pointer cycle.
		// Here we use a struct to memorize the pointer to the first element of the slice
		// and its length.
		ptr := struct {
			ptr any // always an unsafe.Pointer, but avoids a dependency on package unsafe
			len int
		}{v.UnsafePointer(), v.Len()}
		if _, ok := e.ptrSeen[ptr]; ok {
			e.error(&UnsupportedValueError{v, fmt.Sprintf("encountered a cycle via %s", v.Type())})
		}
		e.ptrSeen[ptr] = struct{}{}
		defer delete(e.ptrSeen, ptr)
	}
	se.arrayEnc(e, v, opts)
	e.ptrLevel--
}

func newSliceEncoder(t reflect.Type) encoderFunc {
	// Byte slices get special treatment; arrays don't.
	if t.Elem().Kind() == reflect.Uint8 {
		p := reflect.PointerTo(t.Elem())
		if !p.Implements(marshalerType) && !p.Implements(textMarshalerType) {
			return encodeByteSlice
		}
	}
	enc := sliceEncoder{newArrayEncoder(t)}
	return enc.encode
}

type arrayEncoder struct {
	elemEnc encoderFunc
}

func (ae arrayEncoder) encode(e *encodeState, v reflect.Value, opts encOpts) {
	e.WriteByte('[')
	n := v.Len()
	for i := 0; i < n; i++ {
		if i > 0 {
			e.WriteByte(',')
		}
		ae.elemEnc(e, v.Index(i), opts)
	}
	e.WriteByte(']')
}

func newArrayEncoder(t reflect.Type) encoderFunc {
	enc := arrayEncoder{typeEncoder(t.Elem())}
	return enc.encode
}

type ptrEncoder struct {
	elemEnc encoderFunc
}

func (pe ptrEncoder) encode(e *encodeState, v reflect.Value, opts encOpts) {
	if v.IsNil() {
		e.WriteString("null")
		return
	}
	if e.ptrLevel++; e.ptrLevel > startDetectingCyclesAfter {
		// We're a large number of nested ptrEncoder.encode calls deep;
		// start checking if we've run into a pointer cycle.
		ptr := v.Interface()
		if _, ok := e.ptrSeen[ptr]; ok {
			e.error(&UnsupportedValueError{v, fmt.Sprintf("encountered a cycle via %s", v.Type())})
		}
		e.ptrSeen[ptr] = struct{}{}
		defer delete(e.ptrSeen, ptr)
	}
	pe.elemEnc(e, v.Elem(), opts)
	e.ptrLevel--
}

func newPtrEncoder(t reflect.Type) encoderFunc {
	enc := ptrEncoder{typeEncoder(t.Elem())}
	return enc.encode
}

type condAddrEncoder struct {
	canAddrEnc, elseEnc encoderFunc
}

func (ce condAddrEncoder) encode(e *encodeState, v reflect.Value, opts encOpts) {
	if v.CanAddr() {
		ce.canAddrEnc(e, v, opts)
	} else {
		ce.elseEnc(e, v, opts)
	}
}

// newCondAddrEncoder returns an encoder that checks whether its value
// CanAddr and delegates to canAddrEnc if so, else to elseEnc.
func newCondAddrEncoder(canAddrEnc, elseEnc encoderFunc) encoderFunc {
	enc := condAddrEncoder{canAddrEnc: canAddrEnc, elseEnc: elseEnc}
	return enc.encode
}

func isValidTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
			// Backslash and quote chars are reserved, but
			// otherwise any punctuation chars are allowed
			// in a tag name.
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}

func typeByIndex(t reflect.Type, index []int) reflect.Type {
	for _, i := range index {
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		t = t.Field(i).Type
	}
	return t
}

type reflectWithString struct {
	v  reflect.Value
	ks string
}

func resolveKeyName(k reflect.Value) (string, error) {
	if k.Kind() == reflect.String {
		return k.String(), nil
	}
	if tm, ok := reflect.TypeAssert[encoding.TextMarshaler](k); ok {
		if k.Kind() == reflect.Pointer && k.IsNil() {
			return "", nil
		}
		buf, err := tm.MarshalText()
		return string(buf), err
	}
	switch k.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(k.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(k.Uint(), 10), nil
	}
	panic("unexpected map key type")
}

// stringEscapes selects the escapes appendString writes beyond those every JSON string needs. pig additive (lw-json): the JSON.stringify
// mode writes neither.
type stringEscapes uint8

const (
	escapeHTMLChars      stringEscapes = 1 << iota // <, > and & as \u003c, \u003e and \u0026
	escapeLineSeparators                           // U+2028 and U+2029 as \u2028 and \u2029
	escapeReplacement                              // the U+FFFD that replaces ill-formed UTF-8 as \ufffd
)

// maximalSubpart is the length of the ill-formed sequence at s[i] that a WHATWG UTF-8 decoder (Node's Buffer and TextDecoder) replaces with
// one U+FFFD: a lead byte with the continuation bytes it accepts before the sequence breaks off, or one byte that cannot start a sequence.
func maximalSubpart[Bytes []byte | string](s Bytes, i int) int {
	c := s[i]
	need, lo, hi := 0, byte(0x80), byte(0xBF)
	switch {
	case c >= 0xC2 && c <= 0xDF:
		need = 2
	case c == 0xE0:
		need, lo = 3, 0xA0
	case c == 0xED:
		need, hi = 3, 0x9F
	case c >= 0xE1 && c <= 0xEF:
		need = 3
	case c == 0xF0:
		need, lo = 4, 0x90
	case c >= 0xF1 && c <= 0xF3:
		need = 4
	case c == 0xF4:
		need, hi = 4, 0x8F
	default:
		return 1
	}
	n := 1
	if i+n < len(s) && s[i+n] >= lo && s[i+n] <= hi {
		n++
		for n < need && i+n < len(s) && s[i+n]&0xC0 == 0x80 {
			n++
		}
	}
	return n
}

// The SWAR scan reads eight bytes as one little-endian word: lane k is byte k.
const (
	swarLSB = 0x0101010101010101
	swarMSB = 0x8080808080808080
)

// load8 reads block, eight bytes long, as one word (one load, one bounds check).
func load8[Bytes []byte | string](block Bytes) uint64 {
	_ = block[7]
	return uint64(block[0]) | uint64(block[1])<<8 | uint64(block[2])<<16 | uint64(block[3])<<24 |
		uint64(block[4])<<32 | uint64(block[5])<<40 | uint64(block[6])<<48 | uint64(block[7])<<56
}

// swarZero marks (with the lane's top bit) each zero lane of x. A borrow can mark a lane above a zero lane falsely, never one below, so the
// lowest mark is exact.
func swarZero(x uint64) uint64 { return (x - swarLSB) &^ x }

// stringStopIndex is the index of the first byte at or after i that appendString must look at, or len(src): a byte below 0x20, '"', '\\', a
// byte from 0x80 (the start or part of a multi-byte sequence) and, with html, '<', '>' and '&'. It reads eight bytes at a time; only the
// lowest mark of a word is exact, which is the one it uses.
func stringStopIndex[Bytes []byte | string](src Bytes, i int, html bool) int {
	for ; i+8 <= len(src); i += 8 {
		w := load8(src[i : i+8])
		// (w - 0x20 per lane) &^ w marks a lane below 0x20; w itself marks a lane from 0x80.
		stops := (w-swarLSB*0x20)&^w | swarZero(w^(swarLSB*'"')) | swarZero(w^(swarLSB*'\\')) | w
		if html {
			stops |= swarZero(w^(swarLSB*'<')) | swarZero(w^(swarLSB*'>')) | swarZero(w^(swarLSB*'&'))
		}
		if stops &= swarMSB; stops != 0 {
			return i + bits.TrailingZeros64(stops)>>3
		}
	}
	for ; i < len(src); i++ {
		if b := src[i]; b >= utf8.RuneSelf || !htmlSafeSet[b] && (html || !safeSet[b]) {
			return i
		}
	}
	return i
}

func appendString[Bytes []byte | string](dst []byte, src Bytes, escapes stringEscapes) []byte {
	html := escapes&escapeHTMLChars != 0
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(src); {
		// Skip the bytes that need no escape, eight at a time.
		if i = stringStopIndex(src, i, html); i == len(src) {
			break
		}
		if b := src[i]; b < utf8.RuneSelf {
			if htmlSafeSet[b] || (!html && safeSet[b]) {
				i++
				continue
			}
			dst = append(dst, src[start:i]...)
			switch b {
			case '\\', '"':
				dst = append(dst, '\\', b)
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				// This encodes bytes < 0x20 except for \b, \f, \n, \r and \t.
				// If escapeHTML is set, it also escapes <, >, and &
				// because they can lead to security holes when
				// user-controlled strings are rendered into JSON
				// and served to some browsers.
				dst = append(dst, '\\', 'u', '0', '0', hex[b>>4], hex[b&0xF])
			}
			i++
			start = i
			continue
		}
		// Native editor strings use WTF-8 for lone UTF-16 units. JSON carries those units as escapes, never as invalid UTF-8 bytes.
		if len(src)-i >= 3 && src[i] == 0xed && src[i+1] >= 0xa0 && src[i+1] <= 0xbf && src[i+2] >= 0x80 && src[i+2] <= 0xbf {
			unit := uint16(src[i]&15)<<12 | uint16(src[i+1]&63)<<6 | uint16(src[i+2]&63)
			dst = append(dst, src[start:i]...)
			// A high unit directly followed by a low unit is one code point in a JavaScript string; JSON.stringify writes the character.
			if unit < 0xdc00 && len(src)-i >= 6 && src[i+3] == 0xed && src[i+4] >= 0xb0 && src[i+4] <= 0xbf && src[i+5] >= 0x80 && src[i+5] <= 0xbf {
				low := uint16(src[i+3]&15)<<12 | uint16(src[i+4]&63)<<6 | uint16(src[i+5]&63)
				dst = utf8.AppendRune(dst, 0x10000+(rune(unit)-0xd800)<<10+rune(low)-0xdc00)
				i += 6
				start = i
				continue
			}
			dst = append(dst, '\\', 'u', hex[unit>>12], hex[(unit>>8)&15], hex[(unit>>4)&15], hex[unit&15])
			i += 3
			start = i
			continue
		}
		// Copied from encoding/json, which tracks generic utf8 functionality in https://go.dev/issue/56948.
		// For now, cast only a small portion of byte slices to a string
		// so that it can be stack allocated. This slows down []byte slightly
		// due to the extra copy, but keeps string performance roughly the same.
		n := min(len(src)-i, utf8.UTFMax)
		c, size := utf8.DecodeRuneInString(string(src[i : i+n]))
		if c == utf8.RuneError && size == 1 {
			// pig (LW-JSON-1): one U+FFFD per maximal subpart, as a JavaScript string decoded from these bytes holds, where encoding/json
			// writes one per byte. JSON.stringify writes it raw; the other modes keep the escape.
			dst = append(dst, src[start:i]...)
			if escapes&escapeReplacement != 0 {
				dst = append(dst, `\ufffd`...)
			} else {
				dst = append(dst, "\ufffd"...)
			}
			i += maximalSubpart(src, i)
			start = i
			continue
		}
		// U+2028 is LINE SEPARATOR.
		// U+2029 is PARAGRAPH SEPARATOR.
		// They are both technically valid characters in JSON strings,
		// but don't work in JSONP, which has to be evaluated as JavaScript,
		// and can lead to security holes there. It is valid JSON to
		// escape them, so we do so in every mode but the JSON.stringify one
		// (which writes them raw, as JavaScript does).
		// See https://en.wikipedia.org/wiki/JSON#Safety.
		if escapes&escapeLineSeparators != 0 && (c == '\u2028' || c == '\u2029') {
			dst = append(dst, src[start:i]...)
			dst = append(dst, '\\', 'u', '2', '0', '2', hex[c&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	dst = append(dst, src[start:]...)
	dst = append(dst, '"')
	return dst
}

// A field represents a single field found in a struct.
type field struct {
	name      string
	nameBytes []byte // []byte(name)

	nameNonEsc  string // `"` + name + `":`
	nameEscHTML string // `"` + HTMLEscape(name) + `":`

	tag       bool
	index     []int
	typ       reflect.Type
	omitEmpty bool
	omitZero  bool
	isZero    func(reflect.Value) bool
	quoted    bool

	encoder encoderFunc
}

type isZeroer interface {
	IsZero() bool
}

var isZeroerType = reflect.TypeFor[isZeroer]()

func typeFields(t reflect.Type) structFields {
	// Anonymous fields to explore at the current level and the next.
	current := []field{}
	next := []field{{typ: t}}

	// Count of queued names for current level and the next.
	var count, nextCount map[reflect.Type]int

	// Types already visited at an earlier level.
	visited := map[reflect.Type]bool{}

	// Fields found.
	var fields []field

	// Buffer to run appendHTMLEscape on field names.
	var nameEscBuf []byte

	for len(next) > 0 {
		current, next = next, current[:0]
		count, nextCount = nextCount, map[reflect.Type]int{}

		for _, f := range current {
			if visited[f.typ] {
				continue
			}
			visited[f.typ] = true

			// Scan f.typ for fields to include.
			for i := 0; i < f.typ.NumField(); i++ {
				sf := f.typ.Field(i)
				if sf.Anonymous {
					t := sf.Type
					if t.Kind() == reflect.Pointer {
						t = t.Elem()
					}
					if !sf.IsExported() && t.Kind() != reflect.Struct {
						// Ignore embedded fields of unexported non-struct types.
						continue
					}
					// Do not ignore embedded fields of unexported struct types
					// since they may have exported fields.
				} else if !sf.IsExported() {
					// Ignore unexported non-embedded fields.
					continue
				}
				tag := sf.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, opts := parseTag(tag)
				if !isValidTag(name) {
					name = ""
				}
				index := make([]int, len(f.index)+1)
				copy(index, f.index)
				index[len(f.index)] = i

				ft := sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Pointer {
					// Follow pointer.
					ft = ft.Elem()
				}

				// Only strings, floats, integers, and booleans can be quoted.
				quoted := false
				if opts.Contains("string") {
					switch ft.Kind() {
					case reflect.Bool,
						reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
						reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
						reflect.Float32, reflect.Float64,
						reflect.String:
						quoted = true
					}
				}

				// Record found field and index sequence.
				if name != "" || !sf.Anonymous || ft.Kind() != reflect.Struct {
					tagged := name != ""
					if name == "" {
						name = sf.Name
					}
					field := field{
						name:      name,
						tag:       tagged,
						index:     index,
						typ:       ft,
						omitEmpty: opts.Contains("omitempty"),
						omitZero:  opts.Contains("omitzero"),
						quoted:    quoted,
					}
					field.nameBytes = []byte(field.name)

					// Build nameEscHTML and nameNonEsc ahead of time.
					nameEscBuf = appendHTMLEscape(nameEscBuf[:0], field.nameBytes)
					field.nameEscHTML = `"` + string(nameEscBuf) + `":`
					field.nameNonEsc = `"` + field.name + `":`

					if field.omitZero {
						t := sf.Type
						// Provide a function that uses a type's IsZero method.
						switch {
						case t.Kind() == reflect.Interface && t.Implements(isZeroerType):
							field.isZero = func(v reflect.Value) bool {
								// Avoid panics calling IsZero on a nil interface or
								// non-nil interface with nil pointer.
								return v.IsNil() ||
									(v.Elem().Kind() == reflect.Pointer && v.Elem().IsNil()) ||
									v.Interface().(isZeroer).IsZero()
							}
						case t.Kind() == reflect.Pointer && t.Implements(isZeroerType):
							field.isZero = func(v reflect.Value) bool {
								// Avoid panics calling IsZero on nil pointer.
								return v.IsNil() || v.Interface().(isZeroer).IsZero()
							}
						case t.Implements(isZeroerType):
							field.isZero = func(v reflect.Value) bool {
								return v.Interface().(isZeroer).IsZero()
							}
						case reflect.PointerTo(t).Implements(isZeroerType):
							field.isZero = func(v reflect.Value) bool {
								if !v.CanAddr() {
									// Temporarily box v so we can take the address.
									v2 := reflect.New(v.Type()).Elem()
									v2.Set(v)
									v = v2
								}
								return v.Addr().Interface().(isZeroer).IsZero()
							}
						}
					}

					fields = append(fields, field)
					if count[f.typ] > 1 {
						// If there were multiple instances, add a second,
						// so that the annihilation code will see a duplicate.
						// It only cares about the distinction between 1 and 2,
						// so don't bother generating any more copies.
						fields = append(fields, fields[len(fields)-1])
					}
					continue
				}

				// Record new anonymous struct to explore in next round.
				nextCount[ft]++
				if nextCount[ft] == 1 {
					next = append(next, field{name: ft.Name(), index: index, typ: ft})
				}
			}
		}
	}

	slices.SortFunc(fields, func(a, b field) int {
		// sort field by name, breaking ties with depth, then
		// breaking ties with "name came from json tag", then
		// breaking ties with index sequence.
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		if c := cmp.Compare(len(a.index), len(b.index)); c != 0 {
			return c
		}
		if a.tag != b.tag {
			if a.tag {
				return -1
			}
			return +1
		}
		return slices.Compare(a.index, b.index)
	})

	// Delete all fields that are hidden by the Go rules for embedded fields,
	// except that fields with JSON tags are promoted.

	// The fields are sorted in primary order of name, secondary order
	// of field index length. Loop over names; for each name, delete
	// hidden fields by choosing the one dominant field that survives.
	out := fields[:0]
	for advance, i := 0, 0; i < len(fields); i += advance {
		// One iteration per name.
		// Find the sequence of fields with the name of this first field.
		fi := fields[i]
		name := fi.name
		for advance = 1; i+advance < len(fields); advance++ {
			fj := fields[i+advance]
			if fj.name != name {
				break
			}
		}
		if advance == 1 { // Only one field with this name
			out = append(out, fi)
			continue
		}
		dominant, ok := dominantField(fields[i : i+advance])
		if ok {
			out = append(out, dominant)
		}
	}

	fields = out
	slices.SortFunc(fields, func(i, j field) int {
		return slices.Compare(i.index, j.index)
	})

	for i := range fields {
		f := &fields[i]
		f.encoder = typeEncoder(typeByIndex(t, f.index))
	}
	exactNameIndex := make(map[string]*field, len(fields))
	foldedNameIndex := make(map[string]*field, len(fields))
	for i, field := range fields {
		exactNameIndex[field.name] = &fields[i]
		// For historical reasons, first folded match takes precedence.
		if _, ok := foldedNameIndex[string(foldName(field.nameBytes))]; !ok {
			foldedNameIndex[string(foldName(field.nameBytes))] = &fields[i]
		}
	}
	return structFields{fields, exactNameIndex, foldedNameIndex}
}

// dominantField looks through the fields, all of which are known to
// have the same name, to find the single field that dominates the
// others using Go's embedding rules, modified by the presence of
// JSON tags. If there are multiple top-level fields, the boolean
// will be false: This condition is an error in Go and we skip all
// the fields.
func dominantField(fields []field) (field, bool) {
	// The fields are sorted in increasing index-length order, then by presence of tag.
	// That means that the first field is the dominant one. We need only check
	// for error cases: two fields at top level, either both tagged or neither tagged.
	if len(fields) > 1 && len(fields[0].index) == len(fields[1].index) && fields[0].tag == fields[1].tag {
		return field{}, false
	}
	return fields[0], true
}

var fieldCache sync.Map // map[reflect.Type]structFields

// cachedTypeFields is like typeFields but uses a cache to avoid repeated work.
func cachedTypeFields(t reflect.Type) structFields {
	if f, ok := fieldCache.Load(t); ok {
		return f.(structFields)
	}
	f, _ := fieldCache.LoadOrStore(t, typeFields(t))
	return f.(structFields)
}

func mayAppendQuote(b []byte, quoted bool) []byte {
	if quoted {
		b = append(b, '"')
	}
	return b
}
