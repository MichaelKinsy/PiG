package sdk

import "testing"

// A call made from a frame callback says so; a call from any other goroutine does not, even while the callback runs.
func TestTuiAltFrameMarksOnlyTheCallbackGoroutine(t *testing.T) {
	var registry tuiAltRegistry
	if registry.inFrame() {
		t.Fatal("a goroutine outside every callback is marked in a frame")
	}
	leave := registry.enterFrame()
	if !registry.inFrame() {
		t.Fatal("the callback's goroutine is not marked")
	}
	other := make(chan bool)
	go func() { other <- registry.inFrame() }()
	if <-other {
		t.Fatal("another goroutine is marked while the callback runs")
	}
	inner := registry.enterFrame()
	inner()
	if !registry.inFrame() {
		t.Fatal("leaving a nested callback unmarked the outer one")
	}
	leave()
	if registry.inFrame() {
		t.Fatal("the goroutine stays marked after its callback ended")
	}
}

func TestTuiAltCallbackContextIsPerGoroutine(t *testing.T) {
	var registry tuiAltRegistry
	ext := &Extension{}
	ctx := Context{ext: ext, requestID: "cb1"}
	leave := registry.serveCallback(ctx)
	if got := registry.callContext(ext); got.requestID != "cb1" {
		t.Fatalf("callback goroutine's call context = %q, want cb1", got.requestID)
	}
	other := make(chan string)
	go func() { other <- registry.callContext(ext).requestID }()
	if got := <-other; got != "" {
		t.Fatalf("another goroutine's call context = %q, want none", got)
	}
	leave()
	if got := registry.callContext(ext); got.requestID != "" {
		t.Fatalf("call context after the callback = %q, want none", got.requestID)
	}
}
