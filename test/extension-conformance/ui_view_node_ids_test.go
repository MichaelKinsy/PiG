package extensionconformance

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// TestConformance_UIViewNodeIds pins, in every SDK and realization, that a view node of any kind carries an id, which the
// records of a mount (D614) name a node by. The test hands each row its own token; the fixture mounts a container that
// holds a text and a box, gives each of the three an id built from the token, gives the text and the box a background
// token (the colour member of those two kinds: neither has `color`), and describes the text with an a11y record that
// names its id.
//   - The attached client's snapshot holds the three ids, the two backgrounds and the a11y record, member for member:
//     an id is the row's token, so it is neither a default nor another row's.
//   - An SDK whose kit drops the id of the text sends an a11y record that names a node no view has: the host refuses the
//     mount (`invalid`) and that SDK's rows fail. One that drops the id of the container or the box, or a background,
//     mounts a view that is not the one the client is expected to hold.
//
// That each of the fourteen structural kinds sends its id is pinned by every SDK's own kit test.
func TestConformance_UIViewNodeIds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for i, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		tok := fmt.Sprintf("row%d-%d", i, 7300+53*i)
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			var mu sync.Mutex
			var drawn []uiview.Snapshot
			att, _, aerr := h.bridge.Views().Attach(gridClientAbilities("container", "text", "box"), func(d uiview.Delivery) bool {
				mu.Lock()
				defer mu.Unlock()
				if d.Snapshot.Plugin == tc.extName && d.Snapshot.Mount.Key == "ids" {
					drawn = append(drawn, d.Snapshot)
				}
				return true
			})
			if aerr != nil {
				t.Fatal(aerr)
			}
			t.Cleanup(func() {
				att.Detach()
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			h.ui.ClearRecorded()
			runConformanceCommandArgs(t, h, "ui_view_node_ids", tok)
			pollUntilConformance(t, "the probe never reported its mount", func() bool {
				return slices.ContainsFunc(viewNotifies(h), func(n string) bool { return strings.HasPrefix(n, "view-i-mounted:") })
			})
			if got, want := viewNotifies(h), []string{"view-i-mounted:live"}; !slices.Equal(got, want) {
				t.Fatalf("node id probe reported %q, want %q", got, want)
			}
			mu.Lock()
			sent := slices.Clone(drawn)
			mu.Unlock()
			if len(sent) != 1 {
				t.Fatalf("the client was sent the mount %d times, want once", len(sent))
			}
			wantView := map[string]any{"root": map[string]any{"kind": "container", "id": tok + "-root", "children": []any{
				map[string]any{"kind": "text", "id": tok + "-label", "text": "Label " + tok, "paddingX": 0, "paddingY": 0, "bg": "selectedBg"},
				map[string]any{"kind": "box", "id": tok + "-frame", "paddingX": 1, "paddingY": 0, "bg": "toolPendingBg", "children": []any{
					map[string]any{"kind": "text", "text": "inside", "paddingX": 0, "paddingY": 0},
				}},
			}}}
			if got, want := viewCanonical(t, sent[0].View), viewCanonical(t, wantView); got != want || sent[0].Degraded {
				t.Errorf("the client was sent the view (degraded %v)\n  %s\nwant\n  %s", sent[0].Degraded, got, want)
			}
			wantA11y := []any{map[string]any{"node": tok + "-label", "name": "Label " + tok}}
			if got, want := viewCanonical(t, sent[0].A11y), viewCanonical(t, wantA11y); got != want {
				t.Errorf("the client was sent the a11y records %s, want %s", got, want)
			}
		})
	}
}
