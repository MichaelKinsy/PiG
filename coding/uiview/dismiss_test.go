package uiview

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"
)

// pig additive (D614): one intent of a mount may be declared dismiss. It is the intent a client raises with {} when the
// user dismisses a dialog or overlay mount. It names no node and its arg accepts {}; a second one, a node, an arg that
// refuses {} and a value that is not a boolean are each invalid, with a text that names the intent and the rule.
func TestDismissIntentRules(t *testing.T) {
	a := richAbilities()
	a.Slots = []Slot{SlotPanel, SlotDialog, SlotOverlay}
	closeIntent := func(extra map[string]any) map[string]any {
		d := map[string]any{"name": "fx.close", "arg": map[string]any{"type": "object"}, "dismiss": true}
		maps.Copy(d, extra)
		return d
	}
	tap := map[string]any{"name": "fx.tap", "arg": map[string]any{"type": "object"}}
	record := func(slot string, intents ...any) map[string]any {
		return mountRecord("k", map[string]any{"slot": slot, "intents": intents})
	}
	// The slot is not part of the rule: a client raises the intent only for a dialog or overlay mount, and in another
	// slot the member has no effect. The intent a hidden mount takes may be the same intent.
	for _, slot := range []string{"dialog", "overlay", "panel"} {
		wantOK(t, "one dismiss intent in slot "+slot, tryMount(t, a, record(slot, tap, closeIntent(nil))))
	}
	wantOK(t, "one intent that is dismiss and whenHidden", tryMount(t, a, record("dialog", tap, closeIntent(map[string]any{"whenHidden": true}))))
	wantOK(t, "a dismiss intent beside a whenHidden one", tryMount(t, a, record("dialog", closeIntent(nil), map[string]any{"name": "fx.open", "arg": map[string]any{"type": "object"}, "whenHidden": true})))
	wantOK(t, "dismiss false twice", tryMount(t, a, record("dialog", closeIntent(map[string]any{"dismiss": false}), closeIntent(map[string]any{"name": "fx.close2", "dismiss": false}))))
	needsValue := map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []any{"value"}}
	for what, tc := range map[string]struct {
		intents []any
		text    string
	}{
		"two dismiss intents": {[]any{closeIntent(nil), closeIntent(map[string]any{"name": "fx.close2"})}, `intent "fx.close2": at most one intent of a mount may be dismiss`},
		"dismiss with a node": {[]any{closeIntent(map[string]any{"nodeKey": "n-base"})}, `intent "fx.close" is dismiss and names a node; the user dismisses the mount, not a node`},
		"arg that refuses {}": {[]any{closeIntent(map[string]any{"arg": needsValue})}, `intent "fx.close" is dismiss and its arg does not accept {}: `},
		"scalar arg":          {[]any{closeIntent(map[string]any{"arg": map[string]any{"type": "string"}})}, `intent "fx.close" is dismiss and its arg does not accept {}: `},
		"dismiss not a bool":  {[]any{closeIntent(map[string]any{"dismiss": "escape"})}, `dismiss`},
	} {
		for _, slot := range []string{"dialog", "panel"} {
			err := tryMount(t, a, record(slot, tc.intents...))
			if err == nil || err.Code != CodeInvalid || !strings.Contains(err.Message, tc.text) {
				t.Errorf("%s in slot %s: error = %v, want invalid with %q", what, slot, err, tc.text)
			}
		}
	}
}

// A client that does not know the member is sent the mount like any other: the member is one more optional boolean on
// an intent. This client keeps only the members a client built before the member reads, and the mount is live for it,
// its intents are raised as before, and raising the dismiss intent with {} reaches the plugin's handler, which removes
// the mount.
func TestDismissIntentIsSentToAClientAndRaisedWithTheEmptyObject(t *testing.T) {
	a := richAbilities()
	a.Slots = []Slot{SlotDialog}
	type oldIntent struct {
		Name    string `json:"name"`
		NodeKey string `json:"nodeKey,omitempty"`
	}
	var old []struct {
		State   State       `json:"state"`
		Intents []oldIntent `json:"intents"`
	}
	var sent []Snapshot
	r := New(Options{})
	if _, _, err := r.Attach(a, func(d Delivery) bool {
		raw, err := json.Marshal(d.Snapshot)
		if err != nil {
			t.Error(err)
		}
		old = append(old, struct {
			State   State       `json:"state"`
			Intents []oldIntent `json:"intents"`
		}{})
		if err := json.Unmarshal(raw, &old[len(old)-1]); err != nil {
			t.Errorf("a client that reads only the older members cannot read the snapshot: %v", err)
		}
		sent = append(sent, d.Snapshot)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	args, err := decodeRecord(t, mountRecord("k", map[string]any{"slot": "dialog", "intents": []any{
		map[string]any{"name": "fx.tap", "arg": map[string]any{"type": "object"}},
		map[string]any{"name": "fx.close", "arg": map[string]any{"type": "object"}, "dismiss": true},
	}}))
	wantOK(t, "decode", err)
	st, err := r.Mount("p", args)
	wantOK(t, "mount", err)
	if len(sent) != 1 || sent[0].State != StateLive || len(sent[0].Intents) != 2 || sent[0].Intents[0].Dismiss || !sent[0].Intents[1].Dismiss {
		t.Fatalf("the client was sent %+v, want one live snapshot whose second intent alone is dismiss", sent)
	}
	if got := member(t, sent[0].Intents[1], "dismiss"); got != true {
		t.Fatalf("the dismiss intent's member on the wire = %v, want true", got)
	}
	if got := member(t, sent[0].Intents[0], "dismiss"); got != nil {
		t.Fatalf("a plain intent's dismiss member on the wire = %v, want none", got)
	}
	if len(old) != 1 || old[0].State != StateLive || len(old[0].Intents) != 2 || old[0].Intents[1].Name != "fx.close" {
		t.Fatalf("the client that does not know the member read %+v, want the live mount with both intents", old)
	}
	var got []string
	closes := &fakeInvoker{invoke: func(_ context.Context, inv Invocation) (Result, error) {
		got = append(got, inv.Intent+" "+string(inv.Arg))
		if inv.Intent != "fx.close" {
			return Result{Outcome: OutcomeDone}, nil
		}
		if _, err := r.Transition("p", "remove", RefArgs{Mount: inv.Mount}); err != nil {
			return Result{Outcome: OutcomeFailed, Error: err.Error()}, nil
		}
		return Result{Outcome: OutcomeDone}, nil
	}}
	empty := json.RawMessage(`{}`)
	if res := r.Invoke(context.Background(), "p", st.Mount, "", "fx.tap", empty, closes); res.Outcome != OutcomeDone {
		t.Fatalf("a plain intent of the mount = %+v, want done", res)
	}
	if res := r.Invoke(context.Background(), "p", st.Mount, "", "fx.close", json.RawMessage(`{"x":1}`), closes); res.Outcome != OutcomeRefused {
		t.Fatalf("the dismiss intent with an argument outside its shape = %+v, want refused", res)
	}
	if res := r.Invoke(context.Background(), "p", st.Mount, "", "fx.close", empty, closes); res.Outcome != OutcomeDone {
		t.Fatalf("the dismiss intent with {} = %+v, want done", res)
	}
	if want := []string{"fx.tap {}", "fx.close {}"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("the handler was called with %q, want %q", got, want)
	}
	if now, _ := r.State("p", StateArgs{Key: "k"}); now.State != StateRemoved {
		t.Fatalf("state after the handler removed the mount = %s, want removed", now.State)
	}
	if last := sent[len(sent)-1]; last.State != StateRemoved || old[len(old)-1].State != StateRemoved {
		t.Fatalf("the client's last snapshot is %s (%s for the older client), want removed", last.State, old[len(old)-1].State)
	}
}
