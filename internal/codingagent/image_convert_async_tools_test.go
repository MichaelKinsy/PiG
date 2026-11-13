package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// image-convert.ts ensurePngTranscoder (:78-90) runs onRegistered only after setImageTranscoder, so the re-render it asks for (interactive-mode.ts and
// tool-execution.ts invalidate and requestRender) draws non-PNG images through the transcoder. The Go load is synchronous, so the callback runs inside the call;
// what stays the contract is that the transcoder is registered, and converts, by the time the callback runs, and that a call that registers nothing runs no callback.
func TestEnsurePngTranscoderRegistersBeforeOnRegisteredRuns(t *testing.T) {
	prev := tui.GetCapabilities()
	t.Cleanup(func() {
		tui.SetCapabilities(prev)
		tui.SetImageTranscoder(nil)
	})
	tui.SetImageTranscoder(nil)
	tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolKitty})
	jpeg := jpegWithXmpBeforeOrientation(t)
	calls, registered, drawn := 0, false, ""
	ensurePngTranscoder(func() {
		calls++
		registered = tui.ImageTranscoderRegistered()
		drawn = tui.NewImage(jpeg, "image/jpeg", tui.DefaultImageTheme(), tui.ImageOptions{}, nil).Render(80)[0]
	})
	if calls != 1 || !registered {
		t.Fatalf("onRegistered ran %d times with the transcoder registered=%v, want once, registered", calls, registered)
	}
	if !strings.Contains(drawn, "\x1b_G") {
		t.Fatalf("a non-PNG image drawn from onRegistered kept its text fallback: %q", drawn)
	}
	// Registered already: a second call neither registers nor calls back.
	ensurePngTranscoder(func() { calls++ })
	if calls != 1 {
		t.Fatalf("onRegistered ran again for an existing registration (%d calls)", calls)
	}
}
