package codemode

import (
	"encoding/json"
	"testing"
)

// execute.ts valueText returns the script's string result as it is, and a JavaScript string can hold an unpaired surrogate. PiG carries that unit
// as a WTF-8 sequence, so a string result with "\ud800" keeps it; json.Unmarshal would turn it into U+FFFD.
func TestValueTextKeepsALoneSurrogate(t *testing.T) {
	if got, want := valueText(json.RawMessage(`"\ud800 é"`)), "\xed\xa0\x80 é"; got != want {
		t.Fatalf("valueText = %q, want %q", got, want)
	}
	if got, want := valueText(json.RawMessage(`"a\ud83d\ude00b"`)), "a😀b"; got != want {
		t.Fatalf("a paired surrogate: valueText = %q, want %q", got, want)
	}
}
