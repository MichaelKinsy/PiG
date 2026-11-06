package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// A stored maxBytes between 2^53 and MaxInt is a valid whole byte count; Pi formats it with formatSize
// (1e16 / 1048576 = 9536743164.0625 MB), not the default 50.0KB.
func TestReadTruncationKeepsLargeStoredMaxBytes(t *testing.T) {
	view := readTruncation(map[string]any{"truncation": map[string]any{"truncated": true, "maxBytes": float64(1e16)}})
	if view == nil {
		t.Fatal("readTruncation returned nil")
	}
	if got, want := tools.FormatSize(view.maxBytes), "9536743164.1MB"; got != want {
		t.Fatalf("maxBytes renders %q, want %q", got, want)
	}
}
