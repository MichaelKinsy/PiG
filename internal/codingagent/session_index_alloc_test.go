package codingagent

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// decodeJSONString is what a failed json.Unmarshal into a string leaves: "" for anything that is not null or a string.
func TestDecodeJSONStringMatchesTheDecoder(t *testing.T) {
	for _, raw := range []string{``, `null`, `"anthropic-messages"`, `""`, `"a\nb"`, `"\u00e9"`, "\"\xff\"", `1`, `true`, `{}`, `[]`, ` "x"`} {
		var want string
		wantOK := json.Unmarshal([]byte(raw), &want) == nil
		got, gotOK := decodeJSONString([]byte(raw))
		if !wantOK {
			want = ""
		}
		if got != want || gotOK != wantOK {
			t.Errorf("decodeJSONString(%q) = %q, %v; decoder = %q, %v", raw, got, gotOK, want, wantOK)
		}
	}
}

// A line whose parentId is the id of the line before it shares that string, and the probe is the one a walk without the hint reads.
func TestIndexProbeSharesThePreviousID(t *testing.T) {
	line := []byte(`{"type":"message","id":"b2","parentId":"a1","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"x"}],"timestamp":12}}`)
	plain, ok := walkIndexProbe(line)
	if !ok {
		t.Fatal("walk did not answer")
	}
	for _, previous := range []string{"", "other", "a1"} {
		got, ok := walkIndexProbeAfter(line, previous)
		if !ok || !reflect.DeepEqual(got, plain) {
			t.Fatalf("walk after %q = %+v, %v; want %+v", previous, got, ok, plain)
		}
	}
	previous := string([]byte("a1"))
	got, _ := walkIndexProbeAfter(line, previous)
	if *got.ParentID != previous || len(*got.ParentID) != len(previous) || unsafeStringData(*got.ParentID) != unsafeStringData(previous) {
		t.Errorf("parentId %q does not share the previous id", *got.ParentID)
	}
}

func unsafeStringData(s string) *byte { return unsafe.StringData(s) }
