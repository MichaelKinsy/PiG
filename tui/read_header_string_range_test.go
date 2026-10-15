package tui

import (
	"encoding/json"
	"testing"
)

// Ports the formatReadLineRange cases of upstream read-line-range-display.test.ts (Pi 91014991, #9887): a model can
// send offset and limit as strings, which count when they are numeric; a malformed range prints nothing.
func TestFormatReadLineRangeMatchesPi(t *testing.T) {
	for _, tc := range []struct {
		offset, limit any
		want          string
	}{
		{25.0, 13.0, ":25-37"},
		{"25", "13", ":25-37"},
		{nil, "13", ":1-13"},
		{"25", nil, ":25"},
		{nil, nil, ""},
		{"abc", 13.0, ""},
		{25.0, "", ""},
		{25.0, "Infinity", ""},
	} {
		if got := FormatReadLineRange(tc.offset, tc.limit); got != tc.want {
			t.Errorf("FormatReadLineRange(%#v, %#v) = %q, want %q", tc.offset, tc.limit, got, tc.want)
		}
	}
}

// read-line-range-display.test.ts "shows string ranges in the TUI": the read header renders the string range.
func TestReadHeaderShowsStringRange(t *testing.T) {
	raw := json.RawMessage(`{"path":"notes.txt","offset":"25","limit":"13"}`)
	if got := stripANSI(FormatReadHeader(raw, "")); got != "read notes.txt:25-37" {
		t.Fatalf("FormatReadHeader = %q", got)
	}
}
