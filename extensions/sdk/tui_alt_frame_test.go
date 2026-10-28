package sdk

import "testing"

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
