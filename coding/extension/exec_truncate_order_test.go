package extension

import (
	"reflect"
	"testing"
)

func TestTruncateStopOrderUnixClosesBeforeKill(t *testing.T) {
	var events []string
	stopOnTruncationOrder(false,
		func() { events = append(events, "close-stdout") },
		func() { events = append(events, "close-stderr") },
		func() { events = append(events, "kill") },
	)
	want := []string{"close-stdout", "close-stderr", "kill"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("unix truncation order = %v, want %v", events, want)
	}
}

func TestTruncateStopOrderWindowsKillsBeforeClose(t *testing.T) {
	var events []string
	stopOnTruncationOrder(true,
		func() { events = append(events, "close-stdout") },
		func() { events = append(events, "close-stderr") },
		func() { events = append(events, "kill") },
	)
	want := []string{"kill", "close-stdout", "close-stderr"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("windows truncation order = %v, want %v", events, want)
	}
}
