package ai

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Raw JSON, as tool result details read back from a session are, validates and copies as the marshaling path does: the same accepted values, the same compact copy that keeps member order, at the root and inside plain containers, and empty or invalid raw JSON still decided by that path.
func TestRawJSONClonesAsTheMarshalingPath(t *testing.T) {
	big := `{"truncation":{"content":"` + strings.Repeat(`line \"quoted\" \u00e9 \ud83d\ude00 \ud800\n`, 2000) + `","truncated":true,"totalLines":6851}}`
	raws := []string{
		`{"z":1,"a":[1, 2 ,{"b":null}],"m":"x"}`,
		" \n{ \"spaced\" : [ true , false ] }\t",
		`"text"`, `12.50`, `-0`, `1e400`, `null`, `[]`, `{}`,
		"{\"invalid utf-8\":\"\xff\"}",
		// The marshaling path's copy does not escape for HTML or JavaScript, as Compact does not.
		"{\"html\":\"<a href=\\\"x\\\">&amp;</a>\",\"separators\":\"\u2028\u2029\"}",
		big,
	}
	for _, raw := range raws {
		for _, value := range []any{json.RawMessage(raw), map[string]any{"details": json.RawMessage(raw), "n": 1}, []any{json.RawMessage(raw)}} {
			normalized, err := normalizeJSONValue(value)
			if err != nil {
				t.Fatalf("%.40q: %v", raw, err)
			}
			want := preserveJSONValueTypes(value, normalized)
			if got := cloneJSONValue(value); !reflect.DeepEqual(got, want) {
				t.Fatalf("%.40q: clone %#v, want %#v", raw, got, want)
			}
			if validateJsonValue(value) != nil {
				t.Fatalf("%.40q: validation failed", raw)
			}
			if !isPlainJSON(value, &plainWalk{}) {
				t.Fatalf("%.40q: the plain check declined valid raw JSON", raw)
			}
		}
	}
	// The copy owns its bytes, also when the original is already compact and copying it changes nothing.
	for _, raw := range raws {
		original := json.RawMessage(raw)
		copied := cloneJSONValue(original).(json.RawMessage)
		for index := range copied {
			copied[index] = 'X'
		}
		if string(original) != raw {
			t.Fatalf("%.40q: the copy shares the original bytes", raw)
		}
	}
	for _, raw := range []json.RawMessage{nil, {}, json.RawMessage(`{"a":`), json.RawMessage(`{} {}`), json.RawMessage(`[1,]`)} {
		if isPlainJSON(raw, &plainWalk{}) {
			t.Errorf("%q: the plain check accepted it", raw)
		}
		if _, ok := clonePlainJSON(raw, &plainWalk{}); ok {
			t.Errorf("%q: the plain clone accepted it", raw)
		}
		_, normalizeErr := normalizeJSONValue(raw)
		if (validateJsonValue(raw) == nil) != (normalizeErr == nil) {
			t.Errorf("%q: validation %v, marshaling path %v", raw, validateJsonValue(raw), normalizeErr)
		}
	}
}
