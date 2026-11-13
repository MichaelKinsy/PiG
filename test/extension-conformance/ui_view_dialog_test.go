package extensionconformance

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// TestConformance_UIViewDialog pins the slot `dialog` of D614 in every SDK and realization. The test hands each row its
// own key (a letter under its own modifiers) and number; the fixture mounts the key `dlg` in the slot `dialog` with the
// title "Chart <number>" and the row's key bound to fx.add with the number, hides the mount and shows it again:
//   - a client that states no dialog slot: with only that client attached the mount is answered `unsupported` in every
//     SDK, the host holds no mount and the client is sent nothing. An SDK that mounted in another slot would report live;
//   - a client that states the slot: it is sent the mount in the slot `dialog` with the row's title (not the key, not an
//     empty title) and the row's binding, then the hidden mount, then the live one; the client without the slot is still
//     sent nothing;
//   - the bound key: the client raises the intent of the binding it was sent, with the argument it was sent, and the
//     extension's handler answers twice the row's number plus the client's argument limit (123), a value only that
//     handler produces from that argument.
func TestConformance_UIViewDialog(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	// The keys of TestConformance_UIViewBindings: none is reserved.
	modifiers := []string{"shift+ctrl+", "alt+", "ctrl+alt+", "shift+", "alt+shift+", "ctrl+", "alt+ctrl+shift+"}
	for i, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		key := modifiers[i%len(modifiers)] + string("ghjkmnqruvx"[i])
		n := 5200 + 41*i
		title := fmt.Sprintf("Chart %d", n)
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
			dialogs := func(c *viewClient) []uiview.Snapshot {
				var out []uiview.Snapshot
				for _, d := range c.deliveries() {
					if d.Snapshot.Mount.Key == "dlg" {
						out = append(out, d.Snapshot)
					}
				}
				return out
			}
			held := func() []uiview.Snapshot {
				return slices.DeleteFunc(views.Snapshots(), func(s uiview.Snapshot) bool { return s.Plugin != tc.extName || s.Mount.Key != "dlg" })
			}
			// probe runs the fixture's command and returns what it reported, once it reported a line that begins with last.
			probe := func(last string) []string {
				t.Helper()
				h.ui.ClearRecorded()
				reported := func() []string {
					return slices.DeleteFunc(viewNotifies(h), func(n string) bool { return !strings.HasPrefix(n, "view-d-") })
				}
				runConformanceCommandArgs(t, h, "ui_view_dialog", fmt.Sprintf("%s %d", key, n))
				pollUntilConformance(t, "the probe never reported "+last, func() bool {
					return slices.ContainsFunc(reported(), func(n string) bool { return strings.HasPrefix(n, last) })
				})
				return reported()
			}

			// The client without the slot. The handler's answer below adds the argument limit the extension holds, so
			// the abilities are replicated before anything is mounted.
			plain := attachViewClient(t, views, tc.extName, *conformanceClientAbilities())
			pollUntilConformance(t, "abilities never replicated", func() bool {
				h.ui.ClearRecorded()
				runConformanceCommand(t, h, "ui_view_abilities")
				return slices.ContainsFunc(viewNotifies(h), func(n string) bool { return strings.HasPrefix(n, "view-abilities:mounts=7,argbytes=123,") })
			})
			if got, want := probe("view-d-"), []string{"view-d-mounted:unsupported"}; !slices.Equal(got, want) {
				t.Fatalf("with a client that states no dialog slot the probe reported %q, want %q", got, want)
			}
			if sent, kept := dialogs(plain), held(); len(sent) != 0 || len(kept) != 0 {
				t.Fatalf("after the refused mount the client was sent %+v and the host holds %+v, want nothing", sent, kept)
			}

			// The client with the slot.
			a := *conformanceClientAbilities()
			a.Slots = []uiview.Slot{uiview.SlotDialog, uiview.SlotStatus}
			client := attachViewClient(t, views, tc.extName, a)
			want := []string{"view-d-mounted:live", "view-d-hidden:hidden", "view-d-shown:live"}
			if got := probe("view-d-shown:"); !slices.Equal(got, want) {
				t.Fatalf("with a client that states the dialog slot the probe reported %q, want %q", got, want)
			}
			sent := dialogs(client)
			states := make([]string, len(sent))
			for j, s := range sent {
				states[j] = fmt.Sprintf("%s:%s:%s", s.Slot, s.State, s.Title)
			}
			mounted := fmt.Sprintf("dialog:live:%s", title)
			if want := []string{mounted, fmt.Sprintf("dialog:hidden:%s", title), mounted}; !slices.Equal(states, want) {
				t.Fatalf("the client that states the dialog slot was sent %q, want %q", states, want)
			}
			if other := dialogs(plain); len(other) != 0 {
				t.Errorf("the client that states no dialog slot was sent %+v, want nothing", other)
			}
			last := sent[len(sent)-1]
			if len(last.Bindings) != 1 {
				t.Fatalf("the client was sent the bindings %+v, want the one of the key %q", last.Bindings, key)
			}
			b := last.Bindings[0]
			arg, err := uiview.Canonical(b.Arg)
			if wantArg := fmt.Sprintf(`{"n":%d}`, n); err != nil || b.Key != key || b.Intent != "fx.add" || string(arg) != wantArg {
				t.Fatalf("the client was sent the binding key=%q intent=%q arg=%s (%v), want key=%q intent=\"fx.add\" arg=%s", b.Key, b.Intent, arg, err, key, wantArg)
			}

			// The bound key: what a client does when the user presses it.
			res := h.bridge.InvokeViewIntentFrom(context.Background(), client.att, tc.extName, last.Mount, "", b.Intent, b.Arg)
			answer, err := uiview.Canonical(res.Answer)
			if wantAnswer := fmt.Sprintf(`{"sum":%d}`, 2*n+123); res.Outcome != uiview.OutcomeDone || err != nil || string(answer) != wantAnswer {
				t.Fatalf("the intent of the bound key was answered %+v (%s, %v), want done with %s", res, answer, err, wantAnswer)
			}
		})
	}
}
