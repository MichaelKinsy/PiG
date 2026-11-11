// SPDX-License-Identifier: MIT

package abitest

import (
	"bytes"
	"strings"
	"testing"
)

func TestScriptParsing(t *testing.T) {
	events, err := ParseScript(strings.NewReader("# c\n\nopen now=5 {\"a\":\"now=2\"}\nmodel_event id=3 now=1.5 []\ntool_done id=4 phase=1 hex:00ff\nclose\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[0].Now != 5 || string(events[0].Payload) != `{"a":"now=2"}` || events[1].ID != 3 || events[2].Phase != 1 || !bytes.Equal(events[2].Payload, []byte{0, 255}) {
		t.Fatalf("parsed %+v", events)
	}
	if got := events[2].Wire(); !bytes.Equal(got, []byte{4, 0, 0, 0, 1, 0, 255}) {
		t.Fatalf("tool_done wire %x", got)
	}
	if _, err := ParseScript(strings.NewReader("nope\n")); err == nil {
		t.Fatal("unknown event accepted")
	}
}

// TestScriptRejectsAnOptionBeyondItsField: phase is one byte, so a value of 256 or more is an error, not a wrapped phase.
func TestScriptRejectsAnOptionBeyondItsField(t *testing.T) {
	for _, line := range []string{"tool_done id=4 phase=256 hex:00", "tool_done id=4 phase=4294967297 hex:00", "tool_done id=4294967296 phase=1 hex:00"} {
		if _, err := ParseScript(strings.NewReader(line + "\n")); err == nil {
			t.Errorf("%q accepted", line)
		}
	}
	events, err := ParseScript(strings.NewReader("tool_done id=4294967295 phase=255 hex:00\n"))
	if err != nil || len(events) != 1 || events[0].ID != 4294967295 || events[0].Phase != 255 {
		t.Fatalf("largest values: %+v, %v", events, err)
	}
}
