package protocol

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"
)

func cborHex(t *testing.T, text string) []byte {
	t.Helper()
	data, err := hex.DecodeString(text)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func requireCborError(t *testing.T, err error, contains string) {
	t.Helper()
	if _, ok := errors.AsType[*CborError](err); !ok {
		t.Fatalf("error=%v, want CborError", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(contains)) {
		t.Fatalf("error=%v, want %q", err, contains)
	}
}
func cborRoundTrip(t *testing.T, value any) any {
	t.Helper()
	wire, err := EncodeCbor(value, CborOptions{})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCbor(wire, CborOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestCbor(t *testing.T) {
	t.Parallel()
	// upstream: packages/protocol/test/cbor/cbor.test.ts:61; vectors at :22-57. Every RFC vector retains its exact wire bytes.
	vectors := []struct {
		value any
		wire  string
	}{
		{nil, "f6"}, {false, "f4"}, {true, "f5"}, {float64(0), "00"}, {float64(1), "01"}, {float64(10), "0a"}, {float64(23), "17"}, {float64(24), "1818"}, {float64(25), "1819"}, {float64(100), "1864"}, {float64(1000), "1903e8"}, {float64(1_000_000), "1a000f4240"}, {float64(1_000_000_000_000), "1b000000e8d4a51000"}, {float64(maxSafeInteger), "1b001fffffffffffff"},
		{float64(-1), "20"}, {float64(-10), "29"}, {float64(-24), "37"}, {float64(-25), "3818"}, {float64(-100), "3863"}, {float64(-1000), "3903e7"}, {float64(-1_000_000), "3a000f423f"}, {float64(-maxSafeInteger), "3b001ffffffffffffe"}, {1.1, "fb3ff199999999999a"}, {math.Copysign(0, -1), "fb8000000000000000"},
		{[]byte{1, 2, 3, 4}, "4401020304"}, {"", "60"}, {"IETF", "6449455446"}, {"ü", "62c3bc"}, {"水", "63e6b0b4"}, {"𐅑", "64f0908591"}, {[]any{}, "80"}, {[]any{float64(1), float64(2), float64(3)}, "83010203"}, {[]any{float64(1), []any{float64(2), float64(3)}, []any{float64(4), float64(5)}}, "8301820203820405"}, {Object{{"a", float64(1)}, {"b", []any{float64(2), float64(3)}}}, "a26161016162820203"},
	}
	for i, test := range vectors {
		t.Run(fmt.Sprintf("encodes and decodes RFC 8949 vector %d", i), func(t *testing.T) {
			t.Parallel()
			encoded, err := EncodeCbor(test.value, CborOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(encoded); got != test.wire {
				t.Fatalf("encoded=%s, want %s", got, test.wire)
			}
			decoded, err := DecodeCbor(cborHex(t, test.wire), CborOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if value, ok := test.value.(float64); ok && value == 0 && math.Signbit(value) {
				number, ok := decoded.(float64)
				if !ok || number != 0 || !math.Signbit(number) {
					t.Fatalf("decoded=%v, want negative zero", decoded)
				}
			} else if !reflect.DeepEqual(decoded, test.value) {
				t.Fatalf("decoded=%#v, want %#v", decoded, test.value)
			}
		})
	}
	// upstream: packages/protocol/test/cbor/cbor.test.ts:69.
	t.Run("omits undefined object properties without omitting falsey values", func(t *testing.T) {
		t.Parallel()
		value := Object{{"omitted", Undefined{}}, {"zero", float64(0)}, {"empty", ""}, {"no", false}, {"nil", nil}}
		want := Object{{"zero", float64(0)}, {"empty", ""}, {"no", false}, {"nil", nil}}
		if got := cborRoundTrip(t, value); !reflect.DeepEqual(got, want) {
			t.Fatalf("got=%#v, want %#v", got, want)
		}
	})
	// upstream: packages/protocol/test/cbor/cbor.test.ts:74.
	t.Run("preserves a leading Unicode BOM and treats __proto__ as data", func(t *testing.T) {
		t.Parallel()
		got, err := DecodeCbor(cborHex(t, "63efbbbf"), CborOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got != "\ufeff" {
			t.Fatalf("BOM=%q", got)
		}
		value := Object{{"__proto__", "safe"}}
		decoded := cborRoundTrip(t, value)
		if !reflect.DeepEqual(decoded, value) {
			t.Fatalf("decoded=%#v, want %#v", decoded, value)
		}
		object, ok := decoded.(Object)
		if !ok {
			t.Fatalf("decoded prototype representation=%T, want Object", decoded)
		}
		own, present := object.Get("__proto__")
		if !present || own != "safe" {
			t.Fatalf("own __proto__=%v, present=%v", own, present)
		}
	})
	// upstream: packages/protocol/test/cbor/cbor.test.ts:84. Go has no array holes; Undefined carries that missing-element state at the codec boundary.
	for _, test := range []struct {
		name  string
		value any
	}{
		{"top-level undefined", Undefined{}}, {"undefined array element", []any{Undefined{}}}, {"array hole", []any{Undefined{}}}, {"NaN", math.NaN()}, {"positive infinity", math.Inf(1)}, {"negative infinity", math.Inf(-1)}, {"unsafe positive integer", float64(maxSafeInteger + 1)}, {"unsafe negative integer", float64(-maxSafeInteger - 1)}, {"bigint", big.NewInt(1)}, {"symbol", Symbol{Description: "unsupported"}}, {"function", func() {}}, {"Date", time.Unix(0, 0)}, {"Map", map[string]any{}},
	} {
		t.Run("rejects unsupported encoder value: "+test.name, func(t *testing.T) {
			t.Parallel()
			_, err := EncodeCbor(test.value, CborOptions{})
			requireCborError(t, err, "")
		})
	}
	// upstream: packages/protocol/test/cbor/cbor.test.ts:102.
	t.Run("rejects maps with enumerable symbol keys", func(t *testing.T) {
		t.Parallel()
		_, err := EncodeCbor(Object{{"valid", true}, {Symbol{Description: "key"}, false}}, CborOptions{})
		requireCborError(t, err, "")
	})
	// upstream: packages/protocol/test/cbor/cbor.test.ts:108.
	t.Run("rejects lossy strings, cycles, and excessive encoder depth", func(t *testing.T) {
		t.Parallel()
		_, err := EncodeCbor("\xed\xa0\x80", CborOptions{})
		requireCborError(t, err, "Unicode")
		cyclic := make([]any, 1)
		cyclic[0] = cyclic
		_, err = EncodeCbor(cyclic, CborOptions{})
		requireCborError(t, err, "cycles")
		var tooDeep any
		for depth := 0; depth <= DefaultMaxCborDepth; depth++ {
			tooDeep = []any{tooDeep}
		}
		_, err = EncodeCbor(tooDeep, CborOptions{})
		requireCborError(t, err, "depth")
	})
	// upstream: packages/protocol/test/cbor/cbor.test.ts:120.
	for _, test := range []struct{ name, wire string }{
		{"empty input", ""}, {"truncated integer", "18"}, {"reserved additional information", "1c"}, {"indefinite byte string", "5f"}, {"indefinite text string", "7f"}, {"indefinite array", "9f"}, {"indefinite map", "bf"}, {"tag", "c000"}, {"undefined", "f7"}, {"unsupported simple value", "e0"}, {"break outside an indefinite item", "ff"}, {"float16", "f93c00"}, {"float32", "fa3f800000"}, {"positive infinity", "fb7ff0000000000000"}, {"NaN", "fb7ff8000000000000"}, {"truncated float64", "fb3ff00000"}, {"truncated byte string", "44010203"}, {"truncated text string", "636162"}, {"truncated array", "8201"}, {"truncated map", "a16161"}, {"trailing data", "0000"}, {"non-string map key", "a10102"}, {"duplicate map key", "a2616101616102"}, {"invalid UTF-8 byte", "61ff"}, {"overlong UTF-8", "62c080"}, {"UTF-8 surrogate", "63eda080"}, {"unsafe positive integer", "1b0020000000000000"}, {"unsafe negative integer", "3b001fffffffffffff"}, {"unsafe integer encoded as float64", "fb4340000000000000"},
	} {
		t.Run("rejects invalid decoder input: "+test.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeCbor(cborHex(t, test.wire), CborOptions{})
			requireCborError(t, err, "")
		})
	}
	// upstream: packages/protocol/test/cbor/cbor.test.ts:154.
	t.Run("enforces depth and declared length limits before traversing values", func(t *testing.T) {
		t.Parallel()
		tooDeep := make([]byte, DefaultMaxCborDepth+2)
		for i := range len(tooDeep) - 1 {
			tooDeep[i] = 0x81
		}
		tooDeep[len(tooDeep)-1] = 0xf6
		_, err := DecodeCbor(tooDeep, CborOptions{})
		requireCborError(t, err, "depth")
		for _, wire := range []string{fmt.Sprintf("5a%08x", DefaultMaxCborByteLength+1), fmt.Sprintf("7a%08x", DefaultMaxCborByteLength+1), fmt.Sprintf("9a%08x", DefaultMaxCborContainerLength+1), fmt.Sprintf("ba%08x", DefaultMaxCborContainerLength+1)} {
			_, err := DecodeCbor(cborHex(t, wire), CborOptions{})
			requireCborError(t, err, "limit")
		}
	})
	// upstream: packages/protocol/test/cbor/cbor.test.ts:169.
	t.Run("supports stricter caller-provided limits", func(t *testing.T) {
		t.Parallel()
		_, err := DecodeCbor(cborHex(t, "83010203"), CborOptions{MaxContainerLength: new(2.0)})
		requireCborError(t, err, "limit")
		_, err = DecodeCbor(cborHex(t, "626162"), CborOptions{MaxByteLength: new(2.0)})
		requireCborError(t, err, "limit")
		_, err = EncodeCbor([]any{1, 2, 3}, CborOptions{MaxContainerLength: new(2.0)})
		requireCborError(t, err, "limit")
		_, err = EncodeCbor("ab", CborOptions{MaxByteLength: new(2.0)})
		requireCborError(t, err, "limit")
	})
}
