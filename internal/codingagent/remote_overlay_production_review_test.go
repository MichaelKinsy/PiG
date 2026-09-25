package codingagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Width must survive buffering and binding; checking it only when the bridge
// receives a frame cannot prevent a later resize from painting cached rows.
func TestRemoteOverlayProductionRejectsCachedTerminalWidth(t *testing.T) {
	for _, termWidth := range []int{120, 100} {
		t.Run(fmt.Sprint(termWidth), func(t *testing.T) {
			var out synchronizedOutput
			ui := tui.NewWithOutput(&out, 120, 40)
			m := &InteractiveMode{tuiInst: ui}
			u := &ExtUIContext{m: m}
			u.RunRemoteOverlay(extension.RemoteOverlayOptions{Overlay: true}, nil, func(h extension.RemoteOverlayHandle) {
				if framed, ok := h.(interface{ UpdateLinesAt([]string, int) }); ok {
					framed.UpdateLinesAt([]string{"cached-for-120"}, 120)
				} else {
					h.UpdateLines([]string{"cached-for-120"})
				}
				ui.SetFixedSize(termWidth, 40)
				h.Close(nil)
			})
			if painted := strings.Contains(out.String(), "cached-for-120"); painted != (termWidth == 120) {
				t.Fatalf("painted=%v at terminal width %d: %q", painted, termWidth, out.String())
			}
		})
	}
}

// Drive the production mount: Pi preserves the unframed 75%-width Doom frame and HUD.
func TestRemoteOverlayProductionDoomGeometry(t *testing.T) {
	var out synchronizedOutput
	m := &InteractiveMode{tuiInst: tui.NewWithOutput(&out, 120, 40)}
	u := &ExtUIContext{m: m}
	var opts extension.RemoteOverlayOptions
	if err := json.Unmarshal([]byte(`{"overlay":true,"overlayOptions":{"width":"75%","maxHeight":"95%","anchor":"center","margin":{"top":1}}}`), &opts); err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 29)
	for i := range 28 {
		lines[i] = strings.Repeat("#", 90)
	}
	lines[28] = "HUD" + strings.Repeat("=", 87)
	u.RunRemoteOverlay(opts, nil, func(h extension.RemoteOverlayHandle) {
		h.UpdateLines(lines)
		h.Close(nil)
	})
	if got := out.String(); !strings.Contains(got, lines[0]) || !strings.Contains(got, lines[28]) || strings.ContainsAny(got, "│┌└") {
		t.Fatalf("production overlay cropped or framed the component: %q", got)
	}
}
