package protocol

// Ports packages/protocol/src/cbor/options.ts.
// Ports packages/protocol/src/cbor/encoder.ts.
// Ports packages/protocol/src/cbor/decoder.ts.

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"slices"
	"strconv"
	"unicode/utf8"
)

const (
	DefaultMaxCborByteLength      = 16 * 1024 * 1024
	DefaultMaxCborContainerLength = 1_000_000
	DefaultMaxCborDepth           = 64
	maxSafeInteger                = 1<<53 - 1
)

// CborError reports a value outside the strict definite-length protocol subset.
type CborError struct{ Message string }

func (err *CborError) Error() string { return err.Message }

// CborOptions preserves omitted limits separately from zero and validates JavaScript numeric option semantics.
type CborOptions struct {
	MaxByteLength      *float64
	MaxContainerLength *float64
	MaxDepth           *float64
}

type cborLimits struct{ bytes, container, depth uint64 }

// Undefined distinguishes an omitted JavaScript property from null. It is not a CBOR value; object properties with this value are omitted.
type Undefined struct{}

// Symbol represents a JavaScript symbol at the validation boundary. Symbols are never accepted as CBOR values or enumerable object keys.
type Symbol struct{ Description string }

// Property is one enumerable own property of an Object. Non-string keys are rejected by the encoder, like enumerable JavaScript symbol keys.
type Property struct {
	Key   any
	Value any
}

// Object preserves JavaScript object insertion order without Go map iteration. Numeric index keys encode first; repeated keys retain their first position and last value.
type Object []Property

// Get returns an own property, including __proto__, without prototype lookup.
func (object Object) Get(key string) (any, bool) {
	for _, property := range slices.Backward(object) {
		if property.Key == key {
			return property.Value, true
		}
	}
	return nil, false
}

func resolveCborOptions(options CborOptions) (cborLimits, error) {
	limits := cborLimits{}
	for _, option := range []struct {
		name              string
		value             *float64
		fallback, maximum uint64
		dest              *uint64
	}{
		{"maxByteLength", options.MaxByteLength, DefaultMaxCborByteLength, math.MaxUint32, &limits.bytes},
		{"maxContainerLength", options.MaxContainerLength, DefaultMaxCborContainerLength, math.MaxUint32, &limits.container},
		{"maxDepth", options.MaxDepth, DefaultMaxCborDepth, 512, &limits.depth},
	} {
		value := float64(option.fallback)
		if option.value != nil {
			value = *option.value
		}
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > float64(option.maximum) || math.Trunc(value) != value {
			return limits, &RangeError{Message: fmt.Sprintf("%s must be an integer between 0 and %d", option.name, option.maximum)}
		}
		*option.dest = uint64(value)
	}
	return limits, nil
}

type cborWriter struct {
	data      []byte
	limits    cborLimits
	ancestors map[uintptr]bool
}

func (writer *cborWriter) write(data ...byte) error {
	if uint64(len(data)) > writer.limits.bytes-uint64(len(writer.data)) {
		return cborFailure("CBOR byte length exceeds configured limit of %d", writer.limits.bytes)
	}
	writer.data = append(writer.data, data...)
	return nil
}

func (writer *cborWriter) argument(major byte, value uint64) error {
	prefix := major << 5
	switch {
	case value < 24:
		return writer.write(prefix | byte(value))
	case value <= math.MaxUint8:
		return writer.write(prefix|24, byte(value))
	case value <= math.MaxUint16:
		return writer.write(prefix|25, byte(value>>8), byte(value))
	case value <= math.MaxUint32:
		return writer.write(prefix|26, byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
	default:
		var data [9]byte
		data[0] = prefix | 27
		binary.BigEndian.PutUint64(data[1:], value)
		return writer.write(data[:]...)
	}
}

func (writer *cborWriter) text(value string) error {
	if uint64(len(value)) > writer.limits.bytes {
		return cborFailure("CBOR text string length exceeds configured limit of %d", writer.limits.bytes)
	}
	if !utf8.ValidString(value) {
		return cborFailure("CBOR text strings must contain valid Unicode scalar values")
	}
	if err := writer.argument(3, uint64(len(value))); err != nil {
		return err
	}
	return writer.write([]byte(value)...)
}

func cborNumber(value any) (float64, bool) {
	number := reflect.ValueOf(value)
	switch number.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(number.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(number.Uint()), true
	case reflect.Float32, reflect.Float64:
		return number.Float(), true
	default:
		return 0, false
	}
}

func (writer *cborWriter) value(value any, depth uint64) error {
	if depth > writer.limits.depth {
		return cborFailure("CBOR nesting depth exceeds configured limit of %d", writer.limits.depth)
	}
	if number, ok := cborNumber(value); ok {
		if math.IsInf(number, 0) || math.IsNaN(number) {
			return cborFailure("CBOR numbers must be finite")
		}
		if math.Trunc(number) == number && !(number == 0 && math.Signbit(number)) {
			if math.Abs(number) > maxSafeInteger {
				return cborFailure("CBOR integers must be safe JavaScript integers")
			}
			if number >= 0 {
				return writer.argument(0, uint64(number))
			}
			return writer.argument(1, uint64(-1-number))
		}
		var data [9]byte
		data[0] = 0xfb
		binary.BigEndian.PutUint64(data[1:], math.Float64bits(number))
		return writer.write(data[:]...)
	}
	switch value := value.(type) {
	case nil:
		return writer.write(0xf6)
	case bool:
		if value {
			return writer.write(0xf5)
		}
		return writer.write(0xf4)
	case string:
		return writer.text(value)
	case []byte:
		if uint64(len(value)) > writer.limits.bytes {
			return cborFailure("CBOR byte string length exceeds configured limit of %d", writer.limits.bytes)
		}
		if err := writer.argument(2, uint64(len(value))); err != nil {
			return err
		}
		return writer.write(value...)
	case []any:
		identity := reflect.ValueOf(value).Pointer()
		if writer.ancestors[identity] {
			return cborFailure("CBOR values must not contain cycles")
		}
		if uint64(len(value)) > writer.limits.container {
			return cborFailure("CBOR array length exceeds configured limit of %d", writer.limits.container)
		}
		writer.ancestors[identity] = true
		defer delete(writer.ancestors, identity)
		if err := writer.argument(4, uint64(len(value))); err != nil {
			return err
		}
		for _, item := range value {
			if _, undefined := item.(Undefined); undefined {
				return cborFailure("CBOR arrays must not contain holes or undefined values")
			}
			if err := writer.value(item, depth+1); err != nil {
				return err
			}
		}
		return nil
	case Object:
		identity := reflect.ValueOf(value).Pointer()
		if writer.ancestors[identity] {
			return cborFailure("CBOR values must not contain cycles")
		}
		entries, err := normalizedCborObject(value)
		if err != nil {
			return err
		}
		if uint64(len(entries)) > writer.limits.container {
			return cborFailure("CBOR map length exceeds configured limit of %d", writer.limits.container)
		}
		writer.ancestors[identity] = true
		defer delete(writer.ancestors, identity)
		if err := writer.argument(5, uint64(len(entries))); err != nil {
			return err
		}
		for _, property := range entries {
			if err := writer.text(property.Key.(string)); err != nil {
				return err
			}
			if err := writer.value(property.Value, depth+1); err != nil {
				return err
			}
		}
		return nil
	default:
		kind := "object"
		switch value.(type) {
		case Undefined:
			kind = "undefined"
		case Symbol:
			kind = "symbol"
		case *big.Int:
			kind = "bigint"
		default:
			if reflect.TypeOf(value).Kind() == reflect.Func {
				kind = "function"
			}
		}
		return cborFailure("Unsupported CBOR value type: %s", kind)
	}
}

func normalizedCborObject(object Object) (Object, error) {
	entries := make(Object, 0, len(object))
	positions := make(map[string]int, len(object))
	for _, property := range object {
		key, ok := property.Key.(string)
		if !ok {
			return nil, cborFailure("CBOR map keys must be strings")
		}
		if index, ok := positions[key]; ok {
			entries[index].Value = property.Value
		} else {
			positions[key] = len(entries)
			entries = append(entries, property)
		}
	}
	entries = slices.DeleteFunc(entries, func(property Property) bool { _, undefined := property.Value.(Undefined); return undefined })
	slices.SortStableFunc(entries, func(a, b Property) int {
		aIndex, bIndex := cborPropertyIndex(a.Key.(string)), cborPropertyIndex(b.Key.(string))
		if aIndex < bIndex {
			return -1
		}
		if aIndex > bIndex {
			return 1
		}
		return 0
	})
	return entries, nil
}

func cborPropertyIndex(key string) uint64 {
	value, err := strconv.ParseUint(key, 10, 32)
	if err == nil && value < math.MaxUint32 && strconv.FormatUint(value, 10) == key {
		return value
	}
	return math.MaxUint32
}

// EncodeCbor encodes the strict, definite-length RFC 8949 subset with finite safe numbers, scalar Unicode strings, byte strings, arrays, and ordered Objects.
func EncodeCbor(value any, options CborOptions) ([]byte, error) {
	limits, err := resolveCborOptions(options)
	if err != nil {
		return nil, err
	}
	writer := cborWriter{data: make([]byte, 0, min(256, limits.bytes)), limits: limits, ancestors: map[uintptr]bool{}}
	if err := writer.value(value, 0); err != nil {
		return nil, err
	}
	return writer.data, nil
}

type cborReader struct {
	data   []byte
	offset int
	limits cborLimits
}

func (reader *cborReader) bytes(length uint64) ([]byte, error) {
	if length > uint64(len(reader.data)-reader.offset) {
		return nil, cborFailure("Truncated CBOR payload")
	}
	data := reader.data[reader.offset : reader.offset+int(length)]
	reader.offset += int(length)
	return data, nil
}
func (reader *cborReader) argument(info byte) (uint64, error) {
	if info < 24 {
		return uint64(info), nil
	}
	var size uint64
	switch info {
	case 24:
		size = 1
	case 25:
		size = 2
	case 26:
		size = 4
	case 27:
		size = 8
	case 31:
		return 0, cborFailure("Indefinite-length CBOR items are not supported")
	default:
		return 0, cborFailure("Malformed CBOR additional information")
	}
	data, err := reader.bytes(size)
	if err != nil {
		return 0, err
	}
	var value uint64
	for _, b := range data {
		value = value<<8 | uint64(b)
	}
	if value > maxSafeInteger {
		return 0, cborFailure("Decoded CBOR integer or length is outside the safe range")
	}
	return value, nil
}
func (reader *cborReader) length(info byte, kind string, limit uint64) (uint64, error) {
	if info == 31 {
		return 0, cborFailure("Indefinite-length CBOR %ss are not supported", kind)
	}
	length, err := reader.argument(info)
	if err != nil {
		return 0, err
	}
	if length > limit {
		return 0, cborFailure("CBOR %s length exceeds configured limit of %d", kind, limit)
	}
	return length, nil
}
func (reader *cborReader) value(depth uint64) (any, error) {
	if depth > reader.limits.depth {
		return nil, cborFailure("CBOR nesting depth exceeds configured limit of %d", reader.limits.depth)
	}
	initial, err := reader.bytes(1)
	if err != nil {
		return nil, err
	}
	major, info := initial[0]>>5, initial[0]&31
	switch major {
	case 0, 1:
		value, err := reader.argument(info)
		if err != nil {
			return nil, err
		}
		if major == 0 {
			return float64(value), nil
		}
		if value == maxSafeInteger {
			return nil, cborFailure("Decoded CBOR integer is outside the safe range")
		}
		return -1 - float64(value), nil
	case 2, 3:
		kind := "byte string"
		if major == 3 {
			kind = "text string"
		}
		length, err := reader.length(info, kind, reader.limits.bytes)
		if err != nil {
			return nil, err
		}
		data, err := reader.bytes(length)
		if err != nil {
			return nil, err
		}
		if major == 2 {
			return append([]byte{}, data...), nil
		}
		if !utf8.Valid(data) {
			return nil, cborFailure("CBOR text string contains invalid UTF-8")
		}
		return string(data), nil
	case 4:
		length, err := reader.length(info, "array", reader.limits.container)
		if err != nil {
			return nil, err
		}
		result := []any{}
		for range length {
			item, err := reader.value(depth + 1)
			if err != nil {
				return nil, err
			}
			result = append(result, item)
		}
		return result, nil
	case 5:
		length, err := reader.length(info, "map", reader.limits.container)
		if err != nil {
			return nil, err
		}
		result := Object{}
		keys := map[string]bool{}
		for range length {
			key, err := reader.value(depth + 1)
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, cborFailure("CBOR map keys must be strings")
			}
			if keys[name] {
				return nil, cborFailure("CBOR map contains a duplicate key")
			}
			keys[name] = true
			value, err := reader.value(depth + 1)
			if err != nil {
				return nil, err
			}
			result = append(result, Property{Key: name, Value: value})
		}
		return normalizedCborObject(result)
	case 6:
		return nil, cborFailure("CBOR tags are not supported")
	case 7:
		switch info {
		case 20:
			return false, nil
		case 21:
			return true, nil
		case 22:
			return nil, nil
		case 27:
			data, err := reader.bytes(8)
			if err != nil {
				return nil, err
			}
			value := math.Float64frombits(binary.BigEndian.Uint64(data))
			if math.IsInf(value, 0) || math.IsNaN(value) {
				return nil, cborFailure("Decoded CBOR number must be finite")
			}
			if math.Trunc(value) == value && math.Abs(value) > maxSafeInteger {
				return nil, cborFailure("Decoded CBOR integer is outside the safe range")
			}
			return value, nil
		case 31:
			return nil, cborFailure("CBOR break marker is not supported")
		default:
			return nil, cborFailure("Unsupported CBOR simple value or floating-point width")
		}
	}
	return nil, cborFailure("Malformed CBOR major type")
}

// DecodeCbor decodes exactly one strict RFC 8949 item. Map keys remain own Object properties, never prototype mutations.
func DecodeCbor(data []byte, options CborOptions) (any, error) {
	limits, err := resolveCborOptions(options)
	if err != nil {
		return nil, err
	}
	if uint64(len(data)) > limits.bytes {
		return nil, cborFailure("CBOR byte length exceeds configured limit of %d", limits.bytes)
	}
	reader := cborReader{data: data, limits: limits}
	value, err := reader.value(0)
	if err != nil {
		return nil, err
	}
	if reader.offset != len(data) {
		return nil, cborFailure("CBOR payload contains trailing data")
	}
	return value, nil
}
func cborFailure(format string, args ...any) error {
	return &CborError{Message: fmt.Sprintf(format, args...)}
}
