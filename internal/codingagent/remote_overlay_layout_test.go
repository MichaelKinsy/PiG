package codingagent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// TestRemoteOverlayLayoutMatchesUpstreamShowOverlay pins the geometry of a
// Node ui.custom({overlay: true, ...}) overlay to upstream pi-tui
// resolveOverlayLayout for the doom-overlay options and for upstream's
// defaults. Expected values are hand-derived from upstream tui.ts.
func TestRemoteOverlayLayoutMatchesUpstreamShowOverlay(t *testing.T) {
	const doom = `{"key":"custom-1","overlay":true,"overlayOptions":{"width":"75%","maxHeight":"95%","anchor":"center","margin":{"top":1}}}`
	doomHeight := func(width int) int { return max(10, width*10/32) + 1 } // frame + HUD/footer
	for _, tc := range []struct {
		name         string
		wire         string
		termW, termH int
		height       int
		want         tui.OverlayGeometry
	}{
		{"doom 120x40", doom, 120, 40, doomHeight(90), tui.OverlayGeometry{Width: 90, Row: 6, Col: 15, MaxHeight: 38, HasMaxHeight: true}},
		{"doom 80x24", doom, 80, 24, doomHeight(60), tui.OverlayGeometry{Width: 60, Row: 3, Col: 10, MaxHeight: 22, HasMaxHeight: true}},
		{"doom 60x16", doom, 60, 16, doomHeight(45), tui.OverlayGeometry{Width: 45, Row: 1, Col: 7, MaxHeight: 15, HasMaxHeight: true}},
		{"default 120x40", `{"key":"custom-1","overlay":true,"overlayOptions":{}}`, 120, 40, 10, tui.OverlayGeometry{Width: 80, Row: 15, Col: 20}},
		{"default 60x20", `{"key":"custom-1","overlay":true,"overlayOptions":{}}`, 60, 20, 10, tui.OverlayGeometry{Width: 60, Row: 5, Col: 0}},
		{"component width", `{"key":"custom-1","overlay":true,"overlayOptions":{"width":40}}`, 100, 30, 6, tui.OverlayGeometry{Width: 40, Row: 12, Col: 30}},
		{"invalid row and col override anchor", `{"overlay":true,"overlayOptions":{"width":20,"margin":2,"anchor":"bottom-right","row":"invalid","col":"invalid"}}`, 100, 30, 4, tui.OverlayGeometry{Width: 20, Row: 13, Col: 40}},
		{"margin number absolute row", `{"key":"custom-1","overlay":true,"overlayOptions":{"width":20,"margin":2,"row":5,"col":"50%"}}`, 100, 30, 4, tui.OverlayGeometry{Width: 20, Row: 5, Col: 40}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var opts extension.RemoteOverlayOptions
			if err := json.Unmarshal([]byte(tc.wire), &opts); err != nil {
				t.Fatal(err)
			}
			o := remoteOverlayTUIOptions(opts)
			if o.Title != "" || o.WidthFraction != 0 || o.HeightFraction != 0 {
				t.Fatalf("upstream overlay got pig modal framing: %+v", o)
			}
			if got := tui.ResolveOverlayGeometry(o, tc.height, tc.termW, tc.termH); got != tc.want {
				t.Fatalf("geometry = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Pig's own SDK fixtures still use the legacy title/fraction modal.
func TestRemoteOverlayLegacyModalFieldsKeepModal(t *testing.T) {
	o := remoteOverlayTUIOptions(extension.RemoteOverlayOptions{Overlay: true, Title: "Focused", WidthFraction: 0.5})
	if o.Title != "Focused" || o.WidthFraction != 0.5 || o.HeightFraction != 0.7 {
		t.Fatalf("legacy modal options = %+v", o)
	}
}
