package extensionconformance

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// extrasClientAbilities is a client that states claims (and one that differs for the panel slot), draws scenes of at
// most 5 shapes and no pixel grid, and delivers pointer input.
func extrasClientAbilities() uiview.ClientAbilities {
	a := *conformanceClientAbilities()
	a.Input.Pointer = true
	a.Claims = []uiview.Claim{
		{Name: "display.colors", Value: []byte(`4242`)},
		{Name: "display.kind", Value: []byte(`"conf-kind"`)},
		{Name: "input.gestures", Value: []byte(`["activate","drag"]`)},
		{Name: "display.hover", Value: []byte(`true`)},
	}
	a.SlotClaims = []uiview.SlotClaims{{Slot: uiview.SlotPanel, Claims: []uiview.Claim{{Name: "display.colors", Value: []byte(`77`)}}}}
	a.Drawing = &uiview.DrawingAbilities{Profiles: []string{uiview.ProfileScene}, Shapes: 5, Pixels: 16}
	return a
}

// TestConformance_UIViewExtras pins the D614 amendment in every SDK, one capability per group of lines:
//   - claims: the replicated claims, slot claims and drawing abilities (4242, conf-kind, 77, 5 and 16 are the host's);
//   - alternatives: the alternative whose predicate tests the panel's own claim is what the client is sent;
//   - drawing, a11y and focus: the client's snapshot carries the scene (as many shapes as the client takes), the region
//     and the entries of the alternative's node;
//   - bounds: one past each bound of the record is `invalid`, one past the client's shape limit `limit`, a profile the
//     client does not draw `unsupported`, in every SDK alike;
//   - diagnostics: the counts of the host's notices, then the 32 kept of the 40 a client reported;
//   - finish result: the result goes to the host and comes back in the reply and in the state.
//
// An SDK that drops a member, never makes a call, or answers from a constant cannot produce these lines.
func TestConformance_UIViewExtras(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			views := h.bridge.Views()
			var mu sync.Mutex
			var got []uiview.Snapshot
			att, _, aerr := views.Attach(extrasClientAbilities(), func(d uiview.Delivery) bool {
				mu.Lock()
				defer mu.Unlock()
				if d.Snapshot.Plugin == tc.extName && d.Snapshot.Mount.Key == "extras" {
					got = append(got, d.Snapshot)
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
			sent := func() []uiview.Snapshot {
				mu.Lock()
				defer mu.Unlock()
				return slices.Clone(got)
			}
			pollUntilConformance(t, "abilities never replicated", func() bool {
				h.ui.ClearRecorded()
				runConformanceCommand(t, h, "ui_view_abilities")
				return slices.Contains(viewNotifies(h), "view-abilities:mounts=7,argbytes=123,slots=panel+status,nodes=text+box+select-list,markdown=true")
			})

			h.ui.ClearRecorded()
			runConformanceCommand(t, h, "ui_view_extras")
			waitViewNotify(t, h, "view-x-bigresult:invalid")
			want := []string{
				"view-x-claims:colors=4242,kind=conf-kind,gestures=activate+drag,hover=true,panel.colors=77,profiles=scene,shapes=5,pixels=16",
				"view-x-mounted:live",
				"view-x-alts:invalid",
				"view-x-nest:invalid",
				"view-x-points:invalid",
				"view-x-shapes:invalid",
				"view-x-a11y:invalid",
				"view-x-over:limit",
				"view-x-pixels:unsupported",
				"view-x-stale:stale",
				"view-x-diag:invalid=5,limit=1,unsupported=1,stale=3",
				"view-x-bigresult:invalid",
			}
			if got := viewNotifies(h); !slices.Equal(got, want) {
				t.Fatalf("extras reported\n  %q\nwant\n  %q", got, want)
			}

			// What the client was sent: once (every later update was refused), the alternative, resolved.
			deliveries := sent()
			if len(deliveries) != 1 {
				t.Fatalf("the client was sent the mount %d times, want 1", len(deliveries))
			}
			s := deliveries[0]
			if s.Alternative != 1 || s.Degraded || len(s.Digest) != 64 || s.Alternatives != nil || !strings.Contains(string(s.View), "fixture-x-alt-77") || strings.Contains(string(s.View), "fixture-x-base") {
				t.Errorf("client snapshot: alternative %d degraded %v digest %q view %s", s.Alternative, s.Degraded, s.Digest, s.View)
			}
			if d := s.Drawing; d == nil || d.Profile != uiview.ProfileScene || len(d.Shapes) != 4 || d.Shapes[0].Kind != "rect" || len(d.Regions) != 1 ||
				d.Regions[0].Intent != "fx.tap" || d.Regions[0].Shape.Width != 4 || !slices.Equal(d.Regions[0].Gestures, []string{"activate"}) || d.Regions[0].Name != "Tap" {
				t.Errorf("client drawing = %+v", s.Drawing)
			}
			// The entry of the node the client draws, without the relation to a node of the view it does not draw.
			if len(s.A11y) != 1 || s.A11y[0].Node != "alt" || s.A11y[0].Name != "Extras 77" || s.A11y[0].Role != "button" || s.A11y[0].Checked == nil || !*s.A11y[0].Checked || len(s.A11y[0].Relations) != 0 {
				t.Errorf("client a11y = %+v", s.A11y)
			}
			if len(s.Focus) != 1 || s.Focus[0] != (uiview.Focus{Node: "alt", Request: "mount", Restore: "previous"}) {
				t.Errorf("client focus = %+v", s.Focus)
			}
			// What the host holds is the whole record the extension sent.
			var whole *uiview.Snapshot
			for _, snap := range views.Snapshots() {
				if snap.Plugin == tc.extName && snap.Mount.Key == "extras" {
					whole = &snap
				}
			}
			if whole == nil || len(whole.Alternatives) != 1 || len(whole.A11y) != 2 || len(whole.A11y[0].Relations) != 1 || whole.A11y[0].Relations[0] != (uiview.Relation{Kind: "describedBy", Target: "base"}) ||
				len(whole.Intents) != 1 || !strings.Contains(string(whole.View), "fixture-x-base") || whole.Mount.Rev != 1 {
				t.Fatalf("host snapshot = %+v", whole)
			}

			// 40 reports of the client join the host's 5 notices; 32 are kept and the newest is last.
			for i := range 40 {
				if err := att.Report(tc.extName, s.Mount, uiview.Notice{Code: uiview.NoticeUndrawn, Severity: uiview.SeverityInfo, Path: fmt.Sprintf("node/%d", i)}); err != nil {
					t.Fatal(err)
				}
			}
			h.ui.ClearRecorded()
			runConformanceCommand(t, h, "ui_view_extras_end")
			waitViewNotify(t, h, "view-x-state:finished,picked=77")
			want = []string{"view-x-diag2:n=32,last=node/39", "view-x-finished:finished,picked=77", "view-x-state:finished,picked=77"}
			if got := viewNotifies(h); !slices.Equal(got, want) {
				t.Fatalf("extras end reported\n  %q\nwant\n  %q", got, want)
			}
			deliveries = sent()
			last := deliveries[len(deliveries)-1]
			if result, err := uiview.Canonical(last.Result); len(deliveries) != 2 || last.State != uiview.StateFinished || err != nil || string(result) != `{"picked":77}` {
				t.Errorf("the client's last snapshot: %d deliveries, state %s, result %s (%v)", len(deliveries), last.State, last.Result, err)
			}
		})
	}
}
