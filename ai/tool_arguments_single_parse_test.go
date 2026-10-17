package ai

import (
	"reflect"
	"testing"
)

// A finished call's arguments that are valid JSON with an object at the top are read once (parseValidJSONObject); every
// other text goes through the repair and partial parse of Pi's parseToolCallArguments and parseStreamingJson. The one
// read must give what the full path gives: the same values, the same member order, the same error. The full path is
// the reference here, run with the fast path bypassed (parseStreamingJsonValue's later candidates).
func TestValidObjectArgumentsReadOnceMatchTheFullParse(t *testing.T) {
	for _, text := range []string{
		`{"a":1}`, ` {"a":1} `, "{\"a\":1}\n", `{"a":"\ud800"}`, `{"a":1,"a":2}`, "{\"a\":\"\xff\"}", `{"a":1e400}`, `{"a":-0}`,
		`{"b":2,"a":{"d":1,"c":2}}`, `{"a":"x\u0000y"}`, `{}`, `{"a":[1,{"z":1,"y":2}]}`, `{"__proto__":{"x":1}}`,
		`{"a":12345678901234567890123}`,
	} {
		fast, fastOrder, ok := parseValidJSONObject(text)
		if !ok {
			t.Fatalf("%q: a valid JSON object did not take the single read", text)
		}
		full, fullOrder, nonObject := parseStreamingJsonValueAfterFastPath(text)
		if nonObject != nil || !reflect.DeepEqual(fast, full) || !reflect.DeepEqual(fastOrder, fullOrder) {
			t.Errorf("%q: single read %v %v, full parse %v %v %s", text, fast, fastOrder, full, fullOrder, nonObject)
		}
	}
	for _, text := range []string{"\ufeff{\"a\":1}", "\u00a0{\"a\":1}", `{"a":1}x`, "{\"a\":\"line\nbreak\"}", `{"a":"\q"}`, `[1]`, `null`, `5`} {
		if _, _, ok := parseValidJSONObject(text); ok {
			t.Errorf("%q: text that is not a valid JSON object took the single read", text)
		}
	}
}
