package packagemanager

import (
	"errors"
	"slices"
	"testing"
)

// Pi packages/coding-agent/src/core/package-manager.ts:899-925 emitProgress / withProgress: the callback is optional; an operation emits "start" (with its message) before it runs, then "complete"
// (no message) when it succeeds, or "error" with the failure's message when it fails, and the failure is thrown on.
func TestWithProgressEmitsStartThenCompleteOrErrorAndReturnsTheFailure(t *testing.T) {
	var events []ProgressEvent
	var callback ProgressCallback = func(event ProgressEvent) { events = append(events, event) }

	ran := false
	err := WithProgress(callback, "install", "npm:pkg", "Installing npm:pkg...", func() error {
		if len(events) != 1 {
			t.Errorf("the operation ran after %d events, want the start event first", len(events))
		}
		ran = true
		return nil
	})
	if err != nil || !ran {
		t.Fatalf("WithProgress = %v (ran %v), want nil", err, ran)
	}
	message := "Installing npm:pkg..."
	want := []ProgressEvent{
		{Type: "start", Action: "install", Source: "npm:pkg", Message: &message},
		{Type: "complete", Action: "install", Source: "npm:pkg"},
	}
	if !slices.EqualFunc(events, want, equalProgress) {
		t.Fatalf("events = %+v, want %+v", events, want)
	}

	events = nil
	failure := errors.New("exit 1")
	err = WithProgress(callback, "update", "git:repo", "Updating...", func() error { return failure })
	if !errors.Is(err, failure) {
		t.Fatalf("WithProgress = %v, want the operation's failure", err)
	}
	reported := "exit 1"
	updating := "Updating..."
	want = []ProgressEvent{
		{Type: "start", Action: "update", Source: "git:repo", Message: &updating},
		{Type: "error", Action: "update", Source: "git:repo", Message: &reported},
	}
	if !slices.EqualFunc(events, want, equalProgress) {
		t.Fatalf("events = %+v, want %+v", events, want)
	}

	// No callback set: the operation still runs and its result returns.
	if err := WithProgress(nil, "install", "x", "m", func() error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("WithProgress without a callback = %v, want the failure", err)
	}
	EmitProgress(nil, ProgressEvent{Type: "start"})
}

func equalProgress(a, b ProgressEvent) bool {
	if a.Type != b.Type || a.Action != b.Action || a.Source != b.Source || (a.Message == nil) != (b.Message == nil) {
		return false
	}
	return a.Message == nil || *a.Message == *b.Message
}
