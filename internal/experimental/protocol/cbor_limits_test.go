package protocol

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

func exactCborError(t *testing.T, err error, want string) {
	t.Helper()
	cbor, ok := errors.AsType[*CborError](err)
	if !ok {
		t.Fatalf("error=%T %v, want CborError %q", err, err, want)
	}
	if cbor.Message != want {
		t.Fatalf("message=%q, want %q", cbor.Message, want)
	}
}

func nestedArrays(depth int) any {
	var value any = nil
	for range depth {
		value = []any{value}
	}
	return value
}

func nestedArrayWire(depth int) []byte {
	return append([]byte(strings.Repeat("\x81", depth)), 0xf6)
}

// Pi: packages/protocol/src/cbor/options.ts:1-52 (defaults, MAX_UINT32, MAX_CONFIGURED_DEPTH, resolveLimit messages).
func TestCborDefaultLimitsAreThePinnedValues(t *testing.T) {
	t.Parallel()
	if DefaultMaxCborByteLength != 16777216 || DefaultMaxCborContainerLength != 1000000 || DefaultMaxCborDepth != 64 {
		t.Fatalf("defaults = %d %d %d", DefaultMaxCborByteLength, DefaultMaxCborContainerLength, DefaultMaxCborDepth)
	}
	for _, test := range []struct{ wire, message string }{
		{"5a01000001", "CBOR byte string length exceeds configured limit of 16777216"},
		{"7a01000001", "CBOR text string length exceeds configured limit of 16777216"},
		{"9a000f4241", "CBOR array length exceeds configured limit of 1000000"},
		{"ba000f4241", "CBOR map length exceeds configured limit of 1000000"},
	} {
		_, err := DecodeCbor(cborHex(t, test.wire), CborOptions{})
		exactCborError(t, err, test.message)
	}
	// The default depth admits 64 nested arrays and rejects 65 in both directions (options.ts:9, encoder.ts:82, decoder.ts:27).
	if _, err := DecodeCbor(nestedArrayWire(64), CborOptions{}); err != nil {
		t.Fatalf("decode depth 64: %v", err)
	}
	_, err := DecodeCbor(nestedArrayWire(65), CborOptions{})
	exactCborError(t, err, "CBOR nesting depth exceeds configured limit of 64")
	if _, err := EncodeCbor(nestedArrays(64), CborOptions{}); err != nil {
		t.Fatalf("encode depth 64: %v", err)
	}
	_, err = EncodeCbor(nestedArrays(65), CborOptions{})
	exactCborError(t, err, "CBOR nesting depth exceeds configured limit of 64")
	// A container at the default limit is accepted and one more element is rejected.
	items := make([]any, DefaultMaxCborContainerLength)
	wire, err := EncodeCbor(items, CborOptions{})
	if err != nil {
		t.Fatalf("encode default container limit: %v", err)
	}
	if got, err := DecodeCbor(wire, CborOptions{}); err != nil || len(got.([]any)) != DefaultMaxCborContainerLength {
		t.Fatalf("decode default container limit: %v", err)
	}
	_, err = EncodeCbor(append(items, nil), CborOptions{})
	exactCborError(t, err, "CBOR array length exceeds configured limit of 1000000")
}

func TestCborOptionsAreValidatedLikeResolveLimit(t *testing.T) {
	t.Parallel()
	const bad = "must be an integer between 0 and "
	for _, test := range []struct {
		name    string
		options CborOptions
		message string
	}{
		{"byte length below zero", CborOptions{MaxByteLength: new(-1.0)}, "maxByteLength " + bad + "4294967295"},
		{"byte length fraction", CborOptions{MaxByteLength: new(1.5)}, "maxByteLength " + bad + "4294967295"},
		{"byte length NaN", CborOptions{MaxByteLength: new(math.NaN())}, "maxByteLength " + bad + "4294967295"},
		{"byte length infinity", CborOptions{MaxByteLength: new(math.Inf(1))}, "maxByteLength " + bad + "4294967295"},
		{"byte length above uint32", CborOptions{MaxByteLength: new(4294967296.0)}, "maxByteLength " + bad + "4294967295"},
		{"container length above uint32", CborOptions{MaxContainerLength: new(4294967296.0)}, "maxContainerLength " + bad + "4294967295"},
		{"container length fraction", CborOptions{MaxContainerLength: new(0.5)}, "maxContainerLength " + bad + "4294967295"},
		{"depth above 512", CborOptions{MaxDepth: new(513.0)}, "maxDepth " + bad + "512"},
		{"depth below zero", CborOptions{MaxDepth: new(-1.0)}, "maxDepth " + bad + "512"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for name, run := range map[string]func() error{
				"encode": func() error { _, err := EncodeCbor(nil, test.options); return err },
				"decode": func() error { _, err := DecodeCbor([]byte{0xf6}, test.options); return err },
			} {
				err := run()
				rangeErr, ok := errors.AsType[*RangeError](err)
				if !ok || rangeErr.Message != test.message {
					t.Fatalf("%s error=%T %v, want RangeError %q", name, err, err, test.message)
				}
			}
		})
	}
	for _, options := range []CborOptions{
		{MaxByteLength: new(4294967295.0)}, {MaxContainerLength: new(4294967295.0)}, {MaxDepth: new(512.0)},
		{MaxContainerLength: new(0.0), MaxDepth: new(0.0)}, {MaxDepth: new(math.Copysign(0, -1))},
	} {
		if _, err := DecodeCbor([]byte{0xf6}, options); err != nil {
			t.Fatalf("options %+v rejected: %v", options, err)
		}
	}
	// Zero is a limit, not an unset option (options.ts uses ??): a one-byte payload exceeds a zero byte limit.
	_, err := DecodeCbor([]byte{0xf6}, CborOptions{MaxByteLength: new(0.0)})
	exactCborError(t, err, "CBOR byte length exceeds configured limit of 0")
}

// Pi: decoder.ts:27 and :147-152, encoder.ts:82,159; limits compare with a strict greater-than so a value equal to the limit is accepted.
func TestCborLimitsAreInclusive(t *testing.T) {
	t.Parallel()
	// A zero depth limit admits scalars only; zero is a real limit, never "unset" (options.ts uses ??).
	zero := CborOptions{MaxDepth: new(0.0)}
	if _, err := DecodeCbor([]byte{0xf6}, zero); err != nil {
		t.Fatal(err)
	}
	_, err := DecodeCbor(cborHex(t, "8100"), zero)
	exactCborError(t, err, "CBOR nesting depth exceeds configured limit of 0")
	_, err = EncodeCbor([]any{float64(0)}, zero)
	exactCborError(t, err, "CBOR nesting depth exceeds configured limit of 0")
	depth2 := CborOptions{MaxDepth: new(2.0)}
	if _, err := DecodeCbor(nestedArrayWire(2), depth2); err != nil {
		t.Fatal(err)
	}
	_, err = DecodeCbor(nestedArrayWire(3), depth2)
	exactCborError(t, err, "CBOR nesting depth exceeds configured limit of 2")
	if _, err := EncodeCbor(nestedArrays(2), depth2); err != nil {
		t.Fatal(err)
	}
	_, err = EncodeCbor(nestedArrays(3), depth2)
	exactCborError(t, err, "CBOR nesting depth exceeds configured limit of 2")
	// Container limit: array and map, decode and encode.
	two := CborOptions{MaxContainerLength: new(2.0)}
	if _, err := DecodeCbor(cborHex(t, "820102"), two); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCbor(cborHex(t, "a2616101616202"), two); err != nil {
		t.Fatal(err)
	}
	_, err = DecodeCbor(cborHex(t, "83010203"), two)
	exactCborError(t, err, "CBOR array length exceeds configured limit of 2")
	_, err = DecodeCbor(cborHex(t, "a3616101616202616303"), two)
	exactCborError(t, err, "CBOR map length exceeds configured limit of 2")
	if _, err := EncodeCbor([]any{float64(1), float64(2)}, two); err != nil {
		t.Fatal(err)
	}
	_, err = EncodeCbor([]any{float64(1), float64(2), float64(3)}, two)
	exactCborError(t, err, "CBOR array length exceeds configured limit of 2")
	if _, err := EncodeCbor(Object{{"a", float64(1)}, {"b", float64(2)}}, two); err != nil {
		t.Fatal(err)
	}
	_, err = EncodeCbor(Object{{"a", float64(1)}, {"b", float64(2)}, {"c", float64(3)}}, two)
	exactCborError(t, err, "CBOR map length exceeds configured limit of 2")
	// Undefined properties do not count toward the map limit (encoder.ts:124-127).
	if _, err := EncodeCbor(Object{{"a", float64(1)}, {"gone", Undefined{}}, {"b", float64(2)}}, two); err != nil {
		t.Fatal(err)
	}
	// Byte limit: the whole payload, every string and the encoder output.
	three := CborOptions{MaxByteLength: new(3.0)}
	if _, err := DecodeCbor(cborHex(t, "626162"), three); err != nil {
		t.Fatal(err)
	}
	_, err = DecodeCbor(cborHex(t, "63616263"), three)
	exactCborError(t, err, "CBOR byte length exceeds configured limit of 3")
	if _, err := DecodeCbor(cborHex(t, "42aabb"), three); err != nil {
		t.Fatal(err)
	}
	four := CborOptions{MaxByteLength: new(4.0)}
	_, err = DecodeCbor(cborHex(t, "44aabbccdd"), four)
	exactCborError(t, err, "CBOR byte length exceeds configured limit of 4")
	one := CborOptions{MaxByteLength: new(1.0)}
	_, err = DecodeCbor(cborHex(t, "4101"), one)
	exactCborError(t, err, "CBOR byte length exceeds configured limit of 1")
	_, err = DecodeCbor(cborHex(t, "4301"), CborOptions{MaxByteLength: new(2.0)})
	exactCborError(t, err, "CBOR byte string length exceeds configured limit of 2")
	if wire, err := EncodeCbor("ab", three); err != nil || len(wire) != 3 {
		t.Fatalf("encode at limit: %v %x", err, wire)
	}
	_, err = EncodeCbor("abc", three)
	exactCborError(t, err, "CBOR byte length exceeds configured limit of 3")
	_, err = EncodeCbor("abcd", CborOptions{MaxByteLength: new(2.0)})
	exactCborError(t, err, "CBOR text string length exceeds configured limit of 2")
	_, err = EncodeCbor([]byte{1, 2, 3}, CborOptions{MaxByteLength: new(2.0)})
	exactCborError(t, err, "CBOR byte string length exceeds configured limit of 2")
	if _, err := EncodeCbor(Object{{"abc", "d"}}, CborOptions{MaxByteLength: new(10.0)}); err != nil {
		t.Fatal(err)
	}
	_, err = EncodeCbor(Object{{"abcdef", "x"}}, CborOptions{MaxByteLength: new(4.0)})
	exactCborError(t, err, "CBOR text string length exceeds configured limit of 4")
}

// Pi: encoder.ts:58-70 writeArgument picks the shortest argument width; decoder.ts:122-146 reads every width.
func TestCborArgumentWidthsAtEveryBoundary(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value float64
		wire  string
	}{
		{23, "17"}, {24, "1818"}, {255, "18ff"}, {256, "190100"}, {65535, "19ffff"}, {65536, "1a00010000"}, {4294967295, "1affffffff"}, {4294967296, "1b0000000100000000"},
		{-24, "37"}, {-25, "3818"}, {-256, "38ff"}, {-257, "390100"}, {-65536, "39ffff"}, {-65537, "3a00010000"}, {-4294967296, "3affffffff"}, {-4294967297, "3b0000000100000000"},
		{0.5, "fb3fe0000000000000"}, {-1.5, "fbbff8000000000000"}, {5e-324, "fb0000000000000001"},
	} {
		encoded, err := EncodeCbor(test.value, CborOptions{})
		if err != nil || fmt.Sprintf("%x", encoded) != test.wire {
			t.Fatalf("encode %v = %x, %v; want %s", test.value, encoded, err, test.wire)
		}
		decoded, err := DecodeCbor(cborHex(t, test.wire), CborOptions{})
		if err != nil || decoded != test.value {
			t.Fatalf("decode %s = %v, %v; want %v", test.wire, decoded, err, test.value)
		}
	}
	// Text and byte string lengths use the same widths.
	for _, size := range []int{23, 24, 255, 256, 65535, 65536} {
		wire, err := EncodeCbor(strings.Repeat("a", size), CborOptions{})
		if err != nil {
			t.Fatal(err)
		}
		head := map[int]string{23: "77", 24: "7818", 255: "78ff", 256: "790100", 65535: "79ffff", 65536: "7a00010000"}[size]
		if got := fmt.Sprintf("%x", wire[:len(head)/2]); got != head || len(wire) != len(head)/2+size {
			t.Fatalf("text length %d header %s, total %d", size, got, len(wire))
		}
		back, err := DecodeCbor(wire, CborOptions{})
		if err != nil || len(back.(string)) != size {
			t.Fatalf("text length %d round trip: %v", size, err)
		}
	}
}

// Pi: decoder.ts exact error messages; the differential suite compares the same messages against Pi's Node package.
func TestCborDecoderErrorMessagesMatchPi(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, wire, message string }{
		{"empty input", "", "Truncated CBOR payload"},
		{"truncated integer", "18", "Truncated CBOR payload"},
		{"reserved additional information", "1c", "Malformed CBOR additional information"},
		{"indefinite byte string", "5f", "Indefinite-length CBOR byte strings are not supported"},
		{"indefinite text string", "7f", "Indefinite-length CBOR text strings are not supported"},
		{"indefinite array", "9f", "Indefinite-length CBOR arrays are not supported"},
		{"indefinite map", "bf", "Indefinite-length CBOR maps are not supported"},
		{"indefinite unsigned", "1f", "Indefinite-length CBOR items are not supported"},
		{"tag", "c000", "CBOR tags are not supported"},
		{"undefined", "f7", "Unsupported CBOR simple value or floating-point width"},
		{"float16", "f93c00", "Unsupported CBOR simple value or floating-point width"},
		{"float32", "fa3f800000", "Unsupported CBOR simple value or floating-point width"},
		{"break marker", "ff", "CBOR break marker is not supported"},
		{"infinity", "fb7ff0000000000000", "Decoded CBOR number must be finite"},
		{"NaN", "fb7ff8000000000000", "Decoded CBOR number must be finite"},
		{"unsafe float integer", "fb4340000000000000", "Decoded CBOR integer is outside the safe range"},
		{"unsafe negative float integer", "fbc340000000000000", "Decoded CBOR integer is outside the safe range"},
		{"truncated float", "fb3ff00000", "Truncated CBOR payload"},
		{"truncated byte string", "44010203", "Truncated CBOR payload"},
		{"truncated array", "8201", "Truncated CBOR payload"},
		{"trailing data", "0000", "CBOR payload contains trailing data"},
		{"non-string key", "a10102", "CBOR map keys must be strings"},
		{"duplicate key", "a2616101616102", "CBOR map contains a duplicate key"},
		{"invalid UTF-8", "61ff", "CBOR text string contains invalid UTF-8"},
		{"surrogate", "63eda080", "CBOR text string contains invalid UTF-8"},
		{"unsafe unsigned", "1b0020000000000000", "Decoded CBOR integer or length is outside the safe range"},
		{"unsafe length", "5b0020000000000000", "Decoded CBOR integer or length is outside the safe range"},
		{"unsafe negative", "3b001fffffffffffff", "Decoded CBOR integer is outside the safe range"},
		{"largest unsafe negative argument", "3b001ffffffffffffe", ""},
		{"largest safe float integer", "fb433fffffffffffff", ""},
		{"smallest safe float integer", "fbc33fffffffffffff", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeCbor(cborHex(t, test.wire), CborOptions{})
			if test.message == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			exactCborError(t, err, test.message)
		})
	}
}

// Pi: encoder.ts:99-176 exact error messages.
func TestCborEncoderErrorMessagesMatchPi(t *testing.T) {
	t.Parallel()
	cyclic := make([]any, 1)
	cyclic[0] = cyclic
	for _, test := range []struct {
		name    string
		value   any
		message string
	}{
		{"NaN", math.NaN(), "CBOR numbers must be finite"},
		{"infinity", math.Inf(1), "CBOR numbers must be finite"},
		{"unsafe integer", float64(1 << 53), "CBOR integers must be safe JavaScript integers"},
		{"lone surrogate", "\xed\xa0\x80", "CBOR text strings must contain valid Unicode scalar values"},
		{"cycle", cyclic, "CBOR values must not contain cycles"},
		{"undefined element", []any{Undefined{}}, "CBOR arrays must not contain holes or undefined values"},
		{"undefined", Undefined{}, "Unsupported CBOR value type: undefined"},
		{"symbol", Symbol{}, "Unsupported CBOR value type: symbol"},
		{"function", func() {}, "Unsupported CBOR value type: function"},
		{"object key", Object{{Symbol{}, 1}}, "CBOR map keys must be strings"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := EncodeCbor(test.value, CborOptions{})
			exactCborError(t, err, test.message)
		})
	}
	// A value shared by two siblings is not a cycle: ancestors are removed on exit (encoder.ts:118-121,137-139).
	shared := []any{float64(1)}
	if _, err := EncodeCbor([]any{shared, shared}, CborOptions{}); err != nil {
		t.Fatalf("shared sibling rejected: %v", err)
	}
	object := Object{{"k", float64(1)}}
	if _, err := EncodeCbor(Object{{"a", object}, {"b", object}}, CborOptions{}); err != nil {
		t.Fatalf("shared object rejected: %v", err)
	}
	// Numeric keys sort first in ascending order, as JavaScript Object.keys does.
	wire, err := EncodeCbor(Object{{"b", float64(1)}, {"10", float64(2)}, {"2", float64(3)}, {"a", float64(4)}}, CborOptions{})
	if err != nil || fmt.Sprintf("%x", wire) != "a461320362313002616201616104" {
		t.Fatalf("key order %x, %v", wire, err)
	}
	// Only canonical array indexes below 2^32-1 sort first; "4294967295" and "01" are ordinary string keys (output of Pi's encodeCbor).
	for _, test := range []struct {
		object Object
		wire   string
	}{
		{Object{{"a", float64(2)}, {"4294967295", float64(1)}, {"5", float64(3)}}, "a36135036161026a3432393439363732393501"},
		{Object{{"b", float64(1)}, {"01", float64(2)}, {"1", float64(3)}}, "a361310361620162303102"},
	} {
		if wire, err := EncodeCbor(test.object, CborOptions{}); err != nil || fmt.Sprintf("%x", wire) != test.wire {
			t.Fatalf("key order %x, %v; want %s", wire, err, test.wire)
		}
	}
}
