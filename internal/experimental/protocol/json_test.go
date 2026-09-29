package protocol

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// The JSON bridge exists because Go Chord carries json.RawMessage where packages/protocol/src/codec.ts:19-30 receives JavaScript values. JSON.parse/stringify ordering and UTF-16 identity remain observable before CBOR validation.
func TestProtocolJSONBridge(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, input, want string }{
		{"null", "null", "null"},
		{"falsey values", `{"z":0,"empty":"","no":false,"nil":null,"xs":[]}`, `{"z":0,"empty":"","no":false,"nil":null,"xs":[]}`},
		{"numeric key order", `{"z":1,"10":10,"2":2,"01":1,"a":false}`, `{"2":2,"10":10,"z":1,"01":1,"a":false}`},
		{"duplicate keys", `{"b":1,"a":0,"b":2}`, `{"b":2,"a":0}`},
		{"prototype is data", `{"__proto__":{"polluted":true},"constructor":null}`, `{"__proto__":{"polluted":true},"constructor":null}`},
		{"negative zero", "-0", "0"},
		{"number rounding", "9007199254740993", "9007199254740992"},
		{"unicode scalar", `"\ud83d\ude00"`, `"😀"`},
		{"lone high surrogate", `"\ud800"`, `"\ud800"`},
		{"lone low surrogate", `"\udc00"`, `"\udc00"`},
		{"distinct surrogate keys", `{"\ud800":1,"\ud801":2}`, `{"\ud800":1,"\ud801":2}`},
		{"literal slash and HTML", `"\/<>&"`, `"/<>&"`},
		{"line separators", "\"\u2028\u2029\"", "\"\u2028\u2029\""},
		{"escapes", `"\b\f\n\r\t\"\\"`, `"\b\f\n\r\t\"\\"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value, err := FromJSON(json.RawMessage(test.input))
			if err != nil {
				t.Fatal(err)
			}
			got, err := ToJSON(value)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("JSON=%s, want %s", got, test.want)
			}
		})
	}
	t.Run("CBOR sees original scalar identity", func(t *testing.T) {
		t.Parallel()
		value, err := FromJSON(json.RawMessage(`"\ud800"`))
		if err != nil {
			t.Fatal(err)
		}
		_, err = EncodeCbor(value, CborOptions{})
		requireCborError(t, err, "Unicode")
		value, err = FromJSON(json.RawMessage("-0"))
		if err != nil {
			t.Fatal(err)
		}
		wire, err := EncodeCbor(value, CborOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(wire); got != "fb8000000000000000" {
			t.Fatalf("negative zero CBOR=%s", got)
		}
		value, err = FromJSON(json.RawMessage("9007199254740993"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = EncodeCbor(value, CborOptions{})
		requireCborError(t, err, "safe")
	})
	for _, input := range []string{"", "null null", "[1,]", `{"a":}`, "1e1000", strings.Repeat("[", 513) + "null" + strings.Repeat("]", 513)} {
		t.Run("reject JSON "+input[:min(len(input), 24)], func(t *testing.T) {
			t.Parallel()
			_, err := FromJSON(json.RawMessage(input))
			assertProtocolError(t, err)
		})
	}
	cyclic := make([]any, 1)
	cyclic[0] = cyclic
	for _, test := range []struct {
		name  string
		value any
	}{{"undefined", Undefined{}}, {"bytes", []byte{1}}, {"non-finite", math.NaN()}, {"undefined property", Object{{"x", Undefined{}}}}, {"cycle", cyclic}} {
		t.Run("reject non-JSON "+test.name, func(t *testing.T) { t.Parallel(); _, err := ToJSON(test.value); assertProtocolError(t, err) })
	}
}
