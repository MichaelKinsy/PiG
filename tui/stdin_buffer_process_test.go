package tui

import (
	"slices"
	"testing"
)

// upstream: packages/tui/src/stdin-buffer.ts:296 process(data): void publishes each complete sequence to the data listeners and a bracketed paste to the paste listeners.
func TestStdinBufferProcessPublishesToListenersAndReturnsNothing(t *testing.T) {
	var buffer StdinBuffer
	var data, paste []string
	buffer.On(StdinBufferEventData, NewStdinBufferListener(func(s string) { data = append(data, s) }))
	buffer.On(StdinBufferEventPaste, NewStdinBufferListener(func(s string) { paste = append(paste, s) }))
	buffer.Process("a\x1b[A\x1b[200~pasted\x1b[201~b")
	if !slices.Equal(data, []string{"a", "\x1b[A", "b"}) || !slices.Equal(paste, []string{"pasted"}) {
		t.Fatalf("data = %q, paste = %q", data, paste)
	}
}

// upstream: packages/tui/src/stdin-buffer.ts:272 StdinBufferEventMap = { data: [string]; paste: [string] }: each event's listeners receive one string.
func TestStdinBufferEventMapIsTheOneStringArgumentOfEachEvent(t *testing.T) {
	var buffer StdinBuffer
	var last StdinBufferEventMap
	buffer.On(StdinBufferEventData, NewStdinBufferListener(func(s string) { last.Data = [1]string{s} }))
	buffer.On(StdinBufferEventPaste, NewStdinBufferListener(func(s string) { last.Paste = [1]string{s} }))
	buffer.Emit(StdinBufferEventData, "x")
	buffer.Emit(StdinBufferEventPaste, "y")
	if last != (StdinBufferEventMap{Data: [1]string{"x"}, Paste: [1]string{"y"}}) {
		t.Fatalf("event arguments = %+v", last)
	}
}
