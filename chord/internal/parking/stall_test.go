package parking

import (
	"strings"
	"testing"
	"time"
)

// A guarded wait that outlasts the Watch duration is reported with the stacks, once, and the wait goes on: the report gives
// nothing back.
func TestGuardReportsAHandlerThatHoldsTheSectionAndGoesOnWaiting(t *testing.T) {
	reported := make(chan string, 2)
	defer Watch(time.Millisecond, func(text string) { reported <- text })()
	signal := New()
	waited := make(chan struct{})
	armed := make(chan struct{})
	go func() {
		defer Guard().Disarm()
		close(armed)
		signal.Wait()
		close(waited)
	}()
	<-armed
	var text string
	select {
	case text = <-reported:
	case <-time.After(10 * time.Second):
		signal.Raise()
		t.Fatal("no stall report")
	}
	if !strings.Contains(text, "held the section") || !strings.Contains(text, "parking.(*Signal).Wait") {
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

// A guard disarmed before the Watch duration passed reports nothing.
func TestGuardDisarmedInTimeReportsNothing(t *testing.T) {
	reported := make(chan string, 1)
	defer Watch(time.Hour, func(text string) { reported <- text })()
	Guard().Disarm()
	if len(reported) != 0 {
		t.Fatal("a report for a guard that did not outlast the duration")
	}
}

// Without Watch (the default) a Guard arms nothing: the report hook is not installed.
func TestGuardWithoutWatchHasNoReport(t *testing.T) {
	if watching.Load() != nil {
		t.Fatal("a stall report is installed by default")
	}
	if allocations := testing.AllocsPerRun(100, func() { Guard().Disarm() }); allocations != 0 {
		t.Fatalf("Guard without Watch allocates %v times, want 0", allocations)
	}
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
