package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Outside interactive mode upstream has no uncaughtException handler, so Node
// prints the thrown Error ("Error: <message>") and its stack, then exits 1.
func TestReportRenderOverflowPrintsUpstreamError(t *testing.T) {
	overflow := &tui.RenderOverflowError{Line: 2, LineWidth: 90, TerminalWidth: 80, LogPath: "/agent/pig-tui-crash.log"}
	var stderr strings.Builder
	if !reportRenderOverflow(overflow, []byte("goroutine 1 [running]:\n"), &stderr) {
		t.Fatal("overflow not reported")
	}
	want := "Error: " + overflow.Error() + "\ngoroutine 1 [running]:\n"
	if stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
	stderr.Reset()
	if reportRenderOverflow(errors.New("other"), nil, &stderr) || reportRenderOverflow("other", nil, &stderr) || stderr.Len() != 0 {
		t.Fatalf("non-overflow panic reported: %q", stderr.String())
	}
}
