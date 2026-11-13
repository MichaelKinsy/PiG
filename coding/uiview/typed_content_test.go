package uiview

import (
	"encoding/json"
	"reflect"
	"testing"
)

// pig additive (D614): a record built in Go is held to the closed shape like a record off the wire. Registry.Mount and
// Registry.Update refuse typed content that binds an unknown key or gesture, a 33rd binding or a reserved key with the
// error DecodeMount and DecodeUpdate give the same record, the refused mount holds nothing, and the refused update
// leaves the mount as it was with the notice the wire path leaves.
func TestTypedContentIsCheckedLikeARecordOffTheWire(t *testing.T) {
	tap := []IntentDecl{{Name: "fx.tap", Arg: Shape{Type: "object"}}}
	view := json.RawMessage(`{"root":{"kind":"text","id":"n","text":"base"}}`)
	many := make([]Binding, 33)
	for i := range many {
		many[i] = Binding{Key: string("abefghijklmnopqrstuvwxy0123456789"[i]), Intent: "fx.tap"}
	}
	violations := map[string][]Binding{
		"unknown key":     {{Key: "hyper", Intent: "fx.tap"}},
		"unknown gesture": {{Gesture: "pinch", Intent: "fx.tap"}},
		"33 bindings":     many,
	}
	for _, row := range ReservedKeys() {
		id := row.Base
		if row.Ctrl {
			id = "ctrl+" + id
		}
		violations["reserved "+id] = []Binding{{Key: id, Intent: "fx.tap"}}
	}
	if len(violations) != 3+len(ReservedKeys()) {
		t.Fatalf("%d violations, want one for each row of the reserved table and three more", len(violations))
	}
	same := func(a, b *Error) bool { return a != nil && b != nil && *a == *b }
	for what, bindings := range violations {
		t.Run(what, func(t *testing.T) {
			bad := Content{View: view, Intents: tap, Bindings: bindings}
			good := Content{View: view, Intents: tap, Bindings: []Binding{{Key: "a", Intent: "fx.tap"}}}

			raw, err := json.Marshal(MountArgs{Key: "k", Slot: SlotPanel, Content: bad})
			if err != nil {
				t.Fatal(err)
			}
			_, wire := DecodeMount(raw)
			wantCode(t, wire, CodeInvalid)
			r := New(Options{})
			if _, _, aerr := r.Attach(richAbilities(), nil); aerr != nil {
				t.Fatal(aerr)
			}
			if _, typed := r.Mount("p", MountArgs{Key: "k", Slot: SlotPanel, Content: bad}); !same(typed, wire) {
				t.Fatalf("the typed mount answered %v, want what the record off the wire is answered: %v", typed, wire)
			}
			if held := r.Snapshots(); len(held) != 0 {
				t.Fatalf("the refused mount left %d mounts", len(held))
			}

			st, merr := r.Mount("p", MountArgs{Key: "k", Slot: SlotPanel, Content: good})
			wantOK(t, "typed mount of a content inside the shape", merr)
			raw, err = json.Marshal(UpdateArgs{Mount: st.Mount, Content: bad})
			if err != nil {
				t.Fatal(err)
			}
			_, wire = DecodeUpdate(raw)
			wantCode(t, wire, CodeInvalid)
			if _, typed := r.Update("p", UpdateArgs{Mount: st.Mount, Content: bad}); !same(typed, wire) {
				t.Fatalf("the typed update answered %v, want what the record off the wire is answered: %v", typed, wire)
			}
			held := r.Snapshots()
			if len(held) != 1 || held[0].Mount != st.Mount || len(held[0].Bindings) != 1 || held[0].Bindings[0].Key != "a" {
				t.Fatalf("after the refused update the registry holds %+v, want the mount as it was", held)
			}

			// The wire path: the transport decodes, is refused and tells the registry.
			w := New(Options{})
			if _, _, aerr := w.Attach(richAbilities(), nil); aerr != nil {
				t.Fatal(aerr)
			}
			wst, werr := w.Mount("p", MountArgs{Key: "k", Slot: SlotPanel, Content: good})
			wantOK(t, "mount", werr)
			if wst.Mount != st.Mount {
				t.Fatalf("the two registries mounted %+v and %+v", st.Mount, wst.Mount)
			}
			w.Rejected("p", "update", raw, wire)
			typed, derr := r.Diagnostics("p", StateArgs{Key: "k"})
			wantOK(t, "diagnostics", derr)
			wired, derr := w.Diagnostics("p", StateArgs{Key: "k"})
			wantOK(t, "diagnostics", derr)
			if len(wired.Notices) != 1 || !reflect.DeepEqual(typed.Notices, wired.Notices) {
				t.Fatalf("the typed update left the notices %+v, want those of the wire path: %+v", typed.Notices, wired.Notices)
			}
		})
	}
}
