package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// viewDismissLine is one snapshot a client was sent, as the members the row pins: the state and revision, and the
// intent the mount declares dismiss with its place among the intents, or `none`.
func viewDismissLine(s *uiview.Snapshot) string {
	dismiss := "none"
	for i, d := range s.Intents {
		if d.Dismiss {
			dismiss = fmt.Sprintf("%s@%d", d.Name, i)
		}
	}
	return fmt.Sprintf("%s rev=%d slot=%s dismiss=%s of %d", s.State, s.Mount.Rev, s.Slot, dismiss, len(s.Intents))
}

// TestConformance_UIViewDismiss pins the dismiss member of an intent (D614) in every SDK and realization. The test
// hands each row its own name for the intent; the fixture mounts a dialog that declares it `dismiss` beside a plain
// intent and then tries four updates:
//   - mount: the client is sent the dialog with exactly that intent marked, second of two. An SDK that drops the member
//     sends `none`, and one that marks an intent by itself marks another place or name;
//   - two, node, arg: a second dismiss intent, a dismiss intent that names a node and one whose argument does not
//     accept {} are `invalid` in every SDK, and none of them reaches a client;
//   - ok: the same two intents in the other order are accepted, and the mark moves with its intent.
//
// Then the test is the client whose user dismisses the dialog: it raises the intent with {} and no node. The answer is
// done and the mount is removed, which only the extension's handler does; the plain intent removes nothing.
func TestConformance_UIViewDismiss(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for i, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		name := fmt.Sprintf("fx.close-%c%d", "ghjkmnqruvx"[i], 31+7*i)
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			views := h.bridge.Views()
			abilities := *conformanceClientAbilities()
			abilities.Slots = []uiview.Slot{uiview.SlotPanel, uiview.SlotStatus, uiview.SlotDialog}
			var mu sync.Mutex
			var got []string
			var last uiview.Snapshot
			att, _, aerr := views.Attach(abilities, func(d uiview.Delivery) bool {
				mu.Lock()
				defer mu.Unlock()
				if d.Snapshot.Plugin == tc.extName && d.Snapshot.Mount.Key == "dismiss" {
					got = append(got, viewDismissLine(&d.Snapshot))
					last = d.Snapshot
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
			sent := func() ([]string, uiview.Snapshot) {
				mu.Lock()
				defer mu.Unlock()
				return slices.Clone(got), last
			}
			h.ui.ClearRecorded()
			runConformanceCommandArgs(t, h, "ui_view_dismiss", name)
			pollUntilConformance(t, "the probe never reported its last update", func() bool {
				return slices.ContainsFunc(viewNotifies(h), func(n string) bool { return strings.HasPrefix(n, "view-d-ok:") })
			})
			wantNotes := []string{"view-d-mounted:live", "view-d-two:invalid", "view-d-node:invalid", "view-d-arg:invalid", "view-d-ok:none"}
			if notes := viewNotifies(h); !slices.Equal(notes, wantNotes) {
				t.Fatalf("the probe reported\n  %q\nwant\n  %q", notes, wantNotes)
			}
			// The mount and the accepted update: a refused update is sent to no client.
			want := []string{
				fmt.Sprintf("live rev=1 slot=dialog dismiss=%s@1 of 2", name),
				fmt.Sprintf("live rev=2 slot=dialog dismiss=%s@0 of 2", name),
			}
			lines, live := sent()
			if !slices.Equal(lines, want) {
				t.Fatalf("the client was sent\n  %q\nwant\n  %q", lines, want)
			}
			for _, snap := range views.Snapshots() {
				if snap.Plugin == tc.extName && snap.Mount.Key == "dismiss" {
					if held := viewDismissLine(&snap); held != want[1] {
						t.Errorf("the host holds %q, want %q", held, want[1])
					}
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			// An argument outside the declared shape is the host's to refuse, as for every intent: the mount stays.
			if res := h.bridge.InvokeViewIntentFrom(ctx, att, tc.extName, live.Mount, "", name, json.RawMessage(`{"by":"test"}`)); res.Outcome != uiview.OutcomeRefused || res.Error != `value has unknown member "by"` {
				t.Fatalf("the dismiss intent with a member = %+v, want refused: value has unknown member \"by\"", res)
			}
			if lines, _ = sent(); !slices.Equal(lines, want) {
				t.Fatalf("after the refused intent the client was sent\n  %q\nwant\n  %q", lines, want)
			}
			// The user dismisses the dialog.
			if res := h.bridge.InvokeViewIntentFrom(ctx, att, tc.extName, live.Mount, "", name, json.RawMessage(`{}`)); res.Outcome != uiview.OutcomeDone {
				t.Fatalf("the dismiss intent with {} = %+v, want done", res)
			}
			want = append(want, fmt.Sprintf("removed rev=2 slot=dialog dismiss=%s@0 of 2", name))
			pollUntilConformance(t, "the client was never sent the removed dialog", func() bool {
				lines, _ = sent()
				return len(lines) >= len(want)
			})
			if !slices.Equal(lines, want) {
				t.Fatalf("after the user dismissed the dialog the client was sent\n  %q\nwant\n  %q", lines, want)
			}
		})
	}
}
