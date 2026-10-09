package tui

import (
	"encoding/json"
	"os"
	"testing"
)

// The fixture is Pi 1.0.4's compositeTuiLine over seeded random base/overlay lines with SGR, OSC 8, cursor markers, CJK,
// emoji, combining marks, tabs, image rows and zero or oversized widths (testdata/composite-line-oracle.mjs).
func TestCompositeTuiLineMatchesPiOracle(t *testing.T) {
	data, err := os.ReadFile("testdata/composite-line-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		BaseLine, OverlayLine              string
		StartCol, OverlayWidth, TotalWidth int
		Out                                string
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for index, c := range cases {
		if got := CompositeTuiLine(c.BaseLine, c.OverlayLine, c.StartCol, c.OverlayWidth, c.TotalWidth); got != c.Out {
			failures++
			if failures <= 8 {
				t.Errorf("case %d %+v\n got %q\nwant %q", index, c, got, c.Out)
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d differ from Pi", failures, len(cases))
	}
}
