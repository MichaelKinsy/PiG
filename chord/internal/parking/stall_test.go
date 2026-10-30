//go:build chordstall

package parking

import (
	"strings"
	"testing"
	"time"
)

// A wait that outlasts StallAfter is reported with the stacks, once, and goes on waiting: the section is never given back
// by the report.
func TestWaitReportsAHandlerThatHoldsTheSectionAndGoesOnWaiting(t *testing.T) {
	after, report := StallAfter, Report
	defer func() { StallAfter, Report = after, report }()
	StallAfter = time.Millisecond
	reported := make(chan string, 2)
	Report = func(text string) { reported <- text }
	signal := New()
	waited := make(chan struct{})
	go func() {
		signal.Wait()
		close(waited)
	}()
	text := <-reported
	if !strings.Contains(text, "held the section") || !strings.Contains(text, "parking.(*Signal).wait") {
		t.Fatalf("report = %q, want the stalled wait and the stacks", text)
	}
	select {
	case <-waited:
		t.Fatal("the report ended the wait")
	default:
	}
	signal.Raise()
	<-waited
	if len(reported) != 0 {
		t.Fatal("a second report")
	}
}
