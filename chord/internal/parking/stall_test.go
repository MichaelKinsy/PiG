package parking

import (
	"strings"
	"testing"
	"time"
)

// A wait that outlasts the Watch duration is reported with the stacks, once, and goes on waiting: the section is never
// given back by the report.
func TestWaitReportsAHandlerThatHoldsTheSectionAndGoesOnWaiting(t *testing.T) {
	reported := make(chan string, 2)
	defer Watch(time.Millisecond, func(text string) { reported <- text })()
	signal := New()
	waited := make(chan struct{})
	go func() {
		signal.Wait()
		close(waited)
	}()
	var text string
	select {
	case text = <-reported:
	case <-time.After(10 * time.Second):
		signal.Raise()
		t.Fatal("no stall report")
	}
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

// Without Watch (the default) a Wait neither reports nor starts a timer: the report hook is not installed.
func TestWaitWithoutWatchHasNoReport(t *testing.T) {
	if watching.Load() != nil {
		t.Fatal("a stall report is installed by default")
	}
	signal := New()
	waited := make(chan struct{})
	go func() {
		signal.Wait()
		close(waited)
	}()
	signal.Raise()
	<-waited
}

// Restoring puts back what Watch replaced.
func TestWatchRestoresThePreviousReport(t *testing.T) {
	restoreOuter := Watch(time.Hour, func(string) {})
	outer := watching.Load()
	restoreInner := Watch(time.Millisecond, func(string) {})
	restoreInner()
	if watching.Load() != outer {
		t.Fatal("the inner restore did not bring back the outer report")
	}
	restoreOuter()
	if watching.Load() != nil {
		t.Fatal("the outer restore did not bring back no report")
	}
}
