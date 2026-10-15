package tui

import (
	"encoding/json"
	"testing"
)

// Guards the null offset/limit fix of upstream read.ts formatReadLineRange
// (.upstream/v0.99.1/packages/coding-agent/src/core/tools/renderers/read.ts:29-33): strict tool schemas make
// models send null for omitted optional fields, and null counts as omitted. Ports the read header assertion of
// tool-execution-component.test.ts:298 ("renders read calls with null offset and limit as full-file reads");
// the rest of that file belongs to the interactive lane.
func TestReadHeadersTreatNullRangeAsOmitted(t *testing.T) {
	raw := json.RawMessage(`{"path":"src/example.ts","offset":null,"limit":null}`)
	if got := stripANSI(FormatReadHeader(raw, "")); got != "read src/example.ts" {
		t.Fatalf("FormatReadHeader = %q", got)
	}
	for name, tc := range map[string]struct{ args, want string }{
		"null offset":  {`{"path":"src/example.ts","offset":null,"limit":5}`, "read src/example.ts:1-5"},
		"null limit":   {`{"path":"src/example.ts","offset":7,"limit":null}`, "read src/example.ts:7"},
		"both present": {`{"path":"src/example.ts","offset":7,"limit":5}`, "read src/example.ts:7-11"},
	} {
		if got := stripANSI(FormatReadHeader(json.RawMessage(tc.args), "")); got != tc.want {
			t.Errorf("%s: FormatReadHeader = %q, want %q", name, got, tc.want)
		}
	}
	if got := readLineRange(nil); got != "" {
		t.Fatalf("readLineRange(nil) = %q", got)
	}
}
