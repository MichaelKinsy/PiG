package codingagent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The no-frontend cost of the component kit (D107) on the ui.custom lines
// path: a ui.custom.render frame of lines decoded off the wire, applied to
// the overlay and drawn. Compare with the same benchmark before the kit
// (spec §1: the cost of every existing extension is unchanged).
func BenchmarkCustomOverlayLinesFrame(b *testing.B) {
	overlay := newCustomOverlay(func() {})
	frames := [2][]byte{}
	for i, row := range []string{"\x1b[7m> Blue in Green\x1b[27m", "\x1b[7m> So What\x1b[27m"} {
		frame, err := json.Marshal(subprocess.RemoteOverlayRenderPayload{Key: "picker", Lines: []string{"Pick a track", row, "  Freddie Freeloader"}, Width: 80, Seq: uint64(i + 1)})
		if err != nil {
			b.Fatal(err)
		}
		frames[i] = frame
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		var payload subprocess.RemoteOverlayRenderPayload
		if err := json.Unmarshal(frames[i&1], &payload); err != nil {
			b.Fatal(err)
		}
		overlay.UpdateLinesAt(payload.Lines, payload.Width)
		_ = overlay.Render(80)
		i++
	}
}
