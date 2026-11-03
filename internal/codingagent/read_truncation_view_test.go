package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// read.ts formatReadResult prints `truncation.maxLines ?? DEFAULT_MAX_LINES` and `truncation.maxBytes ?? DEFAULT_MAX_BYTES`: the read tool's own details
// carry every member, and a limit it did not set reads as the default. The Pi comparison of the card (TestReadWriteCardsMatchPi) draws stored details,
// which arrive as JSON; this test pins the details the tool returns.
func TestReadTruncationViewOfTheToolsOwnDetails(t *testing.T) {
	view := readTruncation(&tools.ReadDetails{Truncation: &tools.TruncationResult{Truncated: true, TruncatedBy: "lines", OutputLines: 3, TotalLines: 9}})
	if view == nil || !view.truncated || view.truncatedBy != "lines" || view.outputLines != "3" || view.totalLines != "9" || view.maxLines != "2000" || view.maxBytes != tools.DefaultMaxBytesUpstream {
		t.Errorf("limits left unset: %+v", view)
	}
	view = readTruncation(&tools.ReadDetails{Truncation: &tools.TruncationResult{Truncated: true, FirstLineExceedsLimit: true, MaxLines: 500, MaxBytes: 1500}})
	if view == nil || !view.firstLineExceedsLimit || view.maxLines != "500" || view.maxBytes != 1500 {
		t.Errorf("limits set: %+v", view)
	}
	if view := readTruncation(&tools.ReadDetails{}); view != nil {
		t.Errorf("details without a truncation: %+v", view)
	}
}
