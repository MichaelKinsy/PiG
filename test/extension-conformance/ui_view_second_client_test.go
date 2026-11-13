package extensionconformance

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// TestConformance_UIViewSecondClient pins, in every SDK and in the isolated, packed and fused realizations, what an
// extension and its clients see when a second client attaches to a session that already has one and a mount (D614).
// That is the terminal and a client of the view endpoint: both attach through Registry.Attach and raise intents
// through the bridge, as this row does. The second client is sent the mount as the first holds it (the same digest),
// the intent it raises is answered done, and the handler's redraw is delivered to both with one digest.
func TestConformance_UIViewSecondClient(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			views := h.bridge.Views()
			first := attachViewClient(t, views, tc.extName, *conformanceClientAbilities())
			const abilities = "view-abilities:mounts=7,argbytes=123,slots=panel+status,nodes=text+box+select-list,markdown=true"
			pollUntilConformance(t, "abilities never replicated", func() bool {
				h.ui.ClearRecorded()
				runConformanceCommand(t, h, "ui_view_abilities")
				return slices.Contains(viewNotifies(h), abilities)
			})
			runConformanceCommand(t, h, "ui_view_redraw")
			waitViewNotify(t, h, "view-redraw:rev=1")
			held := first.deliveries()
			if len(held) == 0 {
				t.Fatal("the first client was not delivered the mount")
			}
			mounted := held[len(held)-1].Snapshot

			// The second client arrives while the mount is live.
			second := &viewClient{}
			att, current, err := views.Attach(*conformanceClientAbilities(), func(d uiview.Delivery) bool {
				second.mu.Lock()
				defer second.mu.Unlock()
				if d.Snapshot.Plugin == tc.extName {
					second.seen = append(second.seen, d)
				}
				return true
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(att.Detach)
			i := slices.IndexFunc(current.Snapshots, func(s uiview.Snapshot) bool { return s.Plugin == tc.extName && s.Mount.Key == "redraw" })
			if i < 0 {
				t.Fatalf("the second client's attach carries no redraw panel: %+v", current.Snapshots)
			}
			if got := current.Snapshots[i]; got.Digest == "" || got.Digest != mounted.Digest || got.Mount != mounted.Mount {
				t.Fatalf("the second client is sent %+v digest %q, the first holds %+v digest %q", got.Mount, got.Digest, mounted.Mount, mounted.Digest)
			}
			// The extension is told nothing new: the merged abilities of two equal clients are those of one.
			h.ui.ClearRecorded()
			runConformanceCommand(t, h, "ui_view_abilities")
			if !slices.Contains(viewNotifies(h), abilities) {
				t.Fatalf("with a second client the extension reads %v, want %s", viewNotifies(h), abilities)
			}

			before := len(first.deliveries())
			res := h.bridge.InvokeViewIntentFrom(context.Background(), att, tc.extName, current.Snapshots[i].Mount, "", "fx.redraw", json.RawMessage(`{"n":20}`))
			if res.Outcome != uiview.OutcomeDone || string(res.Answer) != `{"rev":2}` {
				t.Fatalf("fx.redraw raised by the second client = %+v, want done with rev 2", res)
			}
			a, b := first.deliveries(), second.deliveries()
			if len(a) != before+1 || len(b) != 1 {
				t.Fatalf("after the answer the first client holds %d more deliveries and the second %d, want 1 and 1", len(a)-before, len(b))
			}
			x, y := a[len(a)-1].Snapshot, b[0].Snapshot
			if x.Mount.Rev != 2 || y.Mount != x.Mount || x.Digest == "" || x.Digest != y.Digest || !strings.Contains(string(y.View), "fixture-redraw-143") {
				t.Fatalf("the redraw: first %+v digest %q, second %+v digest %q view %s", x.Mount, x.Digest, y.Mount, y.Digest, y.View)
			}
		})
	}
}
