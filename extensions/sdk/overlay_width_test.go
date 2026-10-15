package sdk

import (
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// A focused overlay component renders at the width upstream TUI.resolveOverlayLayout resolves (tui.ts:1212-1233), as the Node runtime does (coding/extension/host/subprocess/runtime_node_overlay_sizing_test.go uses the same table); an inline component and the legacy titled modal render at the terminal width.
func TestRemoteRenderWidthFollowsUpstreamOverlayLayout(t *testing.T) {
	margin := func(n int) *OverlayMargin { return &OverlayMargin{All: &n} }
	for _, tc := range []struct {
		name    string
		options RemoteOverlayOptions
		want    int
	}{
		{"default", RemoteOverlayOptions{Overlay: true}, 80},
		{"empty layout", RemoteOverlayOptions{Overlay: true, OverlayOptions: &OverlayOptions{}}, 80},
		{"percent width", RemoteOverlayOptions{Overlay: true, OverlayOptions: &OverlayOptions{Width: OverlayPercent(75), Margin: &OverlayMargin{Top: 1}}}, 90},
		{"minWidth clamped after margins", RemoteOverlayOptions{Overlay: true, OverlayOptions: &OverlayOptions{Width: OverlayCells(20), MinWidth: 200, Margin: margin(2)}}, 116},
		{"percentage before margin", RemoteOverlayOptions{Overlay: true, OverlayOptions: &OverlayOptions{Width: OverlayPercent(50), Margin: &OverlayMargin{Left: 10, Right: 10}}}, 60},
		{"zero width", RemoteOverlayOptions{Overlay: true, OverlayOptions: &OverlayOptions{Width: OverlayCells(0)}}, 1},
		{"legacy titled modal", RemoteOverlayOptions{Overlay: true, Title: "Picker"}, 120},
		{"titled overlay with layout", RemoteOverlayOptions{Overlay: true, Title: "Picker", OverlayOptions: &OverlayOptions{Width: OverlayCells(30)}}, 30},
		{"inline", RemoteOverlayOptions{}, 120},
	} {
		encoded, err := json.Marshal(tc.options)
		if err != nil {
			t.Fatal(err)
		}
		var args map[string]any
		if err := json.Unmarshal(encoded, &args); err != nil {
			t.Fatal(err)
		}
		if got := remoteRenderWidth(args)(120); got != tc.want {
			t.Errorf("%s: render width at 120 columns = %d, want %d", tc.name, got, tc.want)
		}
	}
	if got := remoteRenderWidth(map[string]any{"overlay": true, "overlayOptions": map[string]any{"width": "oops"}})(120); got != 80 {
		t.Errorf("invalid width: render width = %d, want 80", got)
	}
}
