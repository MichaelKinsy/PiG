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
