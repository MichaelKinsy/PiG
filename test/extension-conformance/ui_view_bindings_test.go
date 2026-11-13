package extensionconformance

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// TestConformance_UIViewBindings pins the bindings of D614 in every SDK and realization. The test hands each row its
// own key (a letter under its own modifiers) and number; the fixture mounts a view whose bindings are built from them
// and then tries six updates:
//   - mount: the client is sent exactly the three bindings, in order: the row's key with the row's number as its
//     constant argument and a name, a second key that raises the same intent with another argument, and a gesture with
//     no argument and no name. A member an SDK made up (a default argument, a name) or dropped is not what the host holds;
//   - many, reserved, order, gesture, arg: 33 bindings, a reserved key behind a modifier, one key written with its
//     modifiers in two orders, an unknown gesture and an argument outside the intent's shape are `invalid` in every
//     SDK, and none of them reaches a client. The host parses the keys; an SDK that rewrote or dropped a binding would
//     answer `none` to `order`;
//   - ok: two keys that differ only in their letter are accepted, and the host holds them spelled as they were sent.
//
// A record built in Go is held to the same checks: on the registry of the same host, typed content with an unknown key,
// a 33rd binding or a reserved key is answered what the record off the wire is answered.
func TestConformance_UIViewBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	modifiers := []string{"shift+ctrl+", "alt+", "ctrl+alt+", "shift+", "alt+shift+", "ctrl+", "alt+ctrl+shift+"}
	for i, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		key := modifiers[i%len(modifiers)] + string("ghjkmnqruvx"[i])
		n := 4100 + 37*i
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			views := h.bridge.Views()
			var mu sync.Mutex
			var got []uiview.Snapshot
			att, _, aerr := views.Attach(*conformanceClientAbilities(), func(d uiview.Delivery) bool {
				mu.Lock()
				defer mu.Unlock()
				if d.Snapshot.Plugin == tc.extName && d.Snapshot.Mount.Key == "bind" {
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
			h.ui.ClearRecorded()
			runConformanceCommandArgs(t, h, "ui_view_bindings", fmt.Sprintf("%s %d", key, n))
			pollUntilConformance(t, "the probe never reported its last update", func() bool {
				return slices.ContainsFunc(viewNotifies(h), func(n string) bool { return strings.HasPrefix(n, "view-b-ok:") })
			})
			want := []string{"view-b-mounted:live", "view-b-many:invalid", "view-b-reserved:invalid", "view-b-order:invalid", "view-b-gesture:invalid", "view-b-arg:invalid", "view-b-ok:none"}
			if got := viewNotifies(h); !slices.Equal(got, want) {
				t.Fatalf("bindings reported\n  %q\nwant\n  %q", got, want)
			}

			// binding is one binding in a form that compares: the argument canonical, "" when the record has none.
			binding := func(b uiview.Binding) string {
				arg := ""
				if len(b.Arg) > 0 {
					c, err := uiview.Canonical(b.Arg)
					if err != nil {
						t.Fatalf("argument %s: %v", b.Arg, err)
					}
					arg = string(c)
				}
				return fmt.Sprintf("key=%q gesture=%q intent=%q arg=%s name=%q", b.Key, b.Gesture, b.Intent, arg, b.Name)
			}
			bindings := func(s uiview.Snapshot) []string {
				out := make([]string, len(s.Bindings))
				for i, b := range s.Bindings {
					out[i] = binding(b)
				}
				return out
			}
			mu.Lock()
			deliveries := slices.Clone(got)
			mu.Unlock()
			// The mount and the accepted update: a refused update is sent to no client.
			if len(deliveries) != 2 {
				t.Fatalf("the client was sent the mount %d times, want 2", len(deliveries))
			}
			mounted := []string{
				fmt.Sprintf(`key=%q gesture="" intent="fx.go" arg={"n":%d} name="Go %d"`, key, n, n),
				fmt.Sprintf(`key="w" gesture="" intent="fx.go" arg={"n":%d} name=""`, n+1),
				`key="" gesture="swipe-left" intent="fx.tap" arg= name=""`,
			}
			if sent := bindings(deliveries[0]); !slices.Equal(sent, mounted) || len(deliveries[0].Digest) != 64 {
				t.Errorf("the client was sent\n  %q (digest %q)\nwant\n  %q", sent, deliveries[0].Digest, mounted)
			}
			updated := []string{
				fmt.Sprintf(`key="ctrl+shift+p" gesture="" intent="fx.go" arg={"n":%d} name=""`, n),
				fmt.Sprintf(`key="shift+ctrl+q" gesture="" intent="fx.go" arg={"n":%d} name=""`, n+1),
			}
			if sent := bindings(deliveries[1]); !slices.Equal(sent, updated) || deliveries[1].Digest == deliveries[0].Digest {
				t.Errorf("after the update the client was sent\n  %q (digest %q, before %q)\nwant\n  %q", sent, deliveries[1].Digest, deliveries[0].Digest, updated)
			}
			many := make([]uiview.Binding, 33)
			for j := range many {
				many[j] = uiview.Binding{Key: string("abefghijklmnopqrstuvwxy0123456789"[j]), Intent: "fx.tap"}
			}
			for what, bad := range map[string][]uiview.Binding{
				"an unknown key": {{Key: "hyper", Intent: "fx.tap"}}, "a 33rd binding": many, "a reserved key": {{Key: "shift+escape", Intent: "fx.tap"}},
			} {
				content := uiview.Content{
					View:    json.RawMessage(`{"root":{"kind":"text","text":"typed"}}`),
					Intents: []uiview.IntentDecl{{Name: "fx.tap", Arg: uiview.Shape{Type: "object"}}}, Bindings: bad,
				}
				typed := uiview.MountArgs{Key: "typed", Slot: uiview.SlotPanel, Content: content}
				raw, merr := json.Marshal(typed)
				if merr != nil {
					t.Fatal(merr)
				}
				_, wire := uiview.DecodeMount(raw)
				if _, err := views.Mount(tc.extName, typed); wire == nil || wire.Code != uiview.CodeInvalid || err == nil || *err != *wire {
					t.Errorf("typed content with %s was answered %v, want what the record off the wire is answered: %v", what, err, wire)
				}
			}
			// What the host holds is the accepted update, one revision after the mount: the five refused ones changed nothing.
			for _, snap := range views.Snapshots() {
				if snap.Plugin == tc.extName && snap.Mount.Key == "bind" {
					if held := bindings(snap); !slices.Equal(held, updated) || snap.Mount.Rev != 2 {
						t.Errorf("the host holds rev %d\n  %q\nwant rev 2\n  %q", snap.Mount.Rev, held, updated)
					}
					return
				}
			}
			t.Error("the host holds no mount under the key bind")
		})
	}
}
