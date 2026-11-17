package tui

import (
	"runtime"
	"testing"
)

// wheelTranscripts are the transcripts of the wheel frame measurement: newScrollTranscriptAltScreen messages of about 14 rows each, so
// about 1k, 26k and 104k rows of the document.
var wheelTranscripts = []struct {
	name     string
	messages int
}{{"lines-1k", 70}, {"lines-26k", 1800}, {"lines-104k", 7400}}

// wheelFrame is one mouse wheel event over the transcript and the frame it requests (Pi b7bb7ca905b8: a view-only frame). At the top it
// jumps back to the end, like the page benchmark above.
func wheelFrame(tui *TuiAltScreen, transcript *ScrollView) {
	tui.HandleViewportInput("\x1b[<64;10;20M")
	tui.doRender()
	if transcript.ScrollTop() == 0 {
		transcript.ScrollToEnd()
	}
}

// BenchmarkAltScreenWheelScroll is one wheel frame over a long transcript. The bytes and allocations of a frame must not grow with the
// length of the document: the mouse event finds its child by the cached child heights without copying them (Pi's Container.handleMouse
// reads childLines the same way), and the view-only frame renders no component.
func BenchmarkAltScreenWheelScroll(b *testing.B) {
	for _, size := range wheelTranscripts {
		b.Run(size.name, func(b *testing.B) {
			tui, transcript := newScrollTranscriptAltScreen(size.messages)
			b.Cleanup(transcript.Dispose)
			b.ReportAllocs()
			for b.Loop() {
				wheelFrame(tui, transcript)
			}
		})
	}
}

// wheelFrameBytes is the heap bytes one wheel frame allocates, averaged over frames of one transcript.
func wheelFrameBytes(messages, frames int) uint64 {
	tui, transcript := newScrollTranscriptAltScreen(messages)
	defer transcript.Dispose()
	for range 5 {
		wheelFrame(tui, transcript)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range frames {
		wheelFrame(tui, transcript)
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / uint64(frames)
}
