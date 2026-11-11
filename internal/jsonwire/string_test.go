package jsonwire

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestValidAndUnmarshalMatchTheByteForms(t *testing.T) {
	for _, text := range []string{"", " ", "null", `{"a":1}`, `{"a":1`, `{"a":1,"a":2}`, "\"\xff\"", `[1,2,3] x`, `{"index":0,"delta":{"type":"text_delta","text":"streamed text chunk"}}`} {
		if got, want := Valid(text), json.Valid([]byte(text)); got != want {
			t.Errorf("Valid(%q)=%v, json.Valid=%v", text, got, want)
		}
		var viaString, viaBytes any
		errString, errBytes := json.Unmarshal(Bytes(text), &viaString), json.Unmarshal([]byte(text), &viaBytes)
		if (errString == nil) != (errBytes == nil) || (errString != nil && errString.Error() != errBytes.Error()) {
			t.Errorf("Unmarshal(%q) error %v, json.Unmarshal error %v", text, errString, errBytes)
		}
		if errString == nil && !equalAny(viaString, viaBytes) {
			t.Errorf("Unmarshal(%q)=%v, json.Unmarshal=%v", text, viaString, viaBytes)
		}
	}
}

func equalAny(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

// The strings and raw messages a decode stores do not alias the input string.
func TestUnmarshalKeepsNoReferenceToTheString(t *testing.T) {
	input := strings.Clone(`{"s":"abcdef","r":{"k":1}}`)
	var out struct {
		S string          `json:"s"`
		R json.RawMessage `json:"r"`
	}
	if err := json.Unmarshal(Bytes(input), &out); err != nil {
		t.Fatal(err)
	}
	if out.S != "abcdef" || string(out.R) != `{"k":1}` {
		t.Fatalf("decoded %+v", out)
	}
	if len(out.S) > 0 && strings.Contains(input, out.S) && &[]byte(out.S)[0] == &Bytes(input)[strings.Index(input, out.S)] {
		t.Fatal("decoded string shares the input's memory")
	}
}

func TestValidDoesNotAllocate(t *testing.T) {
	text := strings.Repeat(" ", 40) + `{"index":0,"delta":{"type":"text_delta","text":"streamed text chunk"}}`
	if allocs := testing.AllocsPerRun(100, func() {
		if !Valid(text) {
			t.Fatal("invalid")
		}
	}); allocs != 0 {
		t.Fatalf("Valid allocated %v times", allocs)
	}
}
