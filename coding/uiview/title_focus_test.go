package uiview

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"
)

// The tests of this file read the new members through the wire records, so they compile against a registry without them.

func member(t *testing.T, v any, name string) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m[name]
}

// pig additive (D614): a mount's title is at most 64 bytes without a control character, the client is sent it, it is
// part of the digest, and an update replaces it like the rest of the content.
func TestTitleBoundsDeliveryAndDigest(t *testing.T) {
	a := richAbilities()
	wantOK(t, "64-byte title", tryMount(t, a, mountRecord("k", map[string]any{"title": strings.Repeat("t", 64)})))
	wantCode(t, tryMount(t, a, mountRecord("k", map[string]any{"title": strings.Repeat("t", 65)})), CodeInvalid)
	wantOK(t, "wide and combined characters", tryMount(t, a, mountRecord("k", map[string]any{"title": "\u90f5\u4fbf e\u0301 \U0001F468\u200d\U0001F469\u200d\U0001F467"})))
	for what, title := range map[string]any{
		"ESC": "Mail\x1b[31m", "newline": "Mail\nbox", "tab": "Mail\tbox", "DEL": "Mail\x7f", "C1 next line": "Mail\u0085box",
		"C1 CSI": "Mail\u009b31m", "line separator": "Mail\u2028box", "paragraph separator": "Mail\u2029box", "not a string": 7,
	} {
		if err := tryMount(t, a, mountRecord("k", map[string]any{"title": title})); err == nil || err.Code != CodeInvalid {
			t.Errorf("title with %s: error = %v, want invalid", what, err)
		}
	}

	r := New(Options{})
	c, _, _ := attach(t, r, a)
	args, err := decodeRecord(t, mountRecord("k", map[string]any{"title": "Mail (3)"}))
	wantOK(t, "decode", err)
	st, err := r.Mount("p", args)
	wantOK(t, "mount", err)
	first := c.got[len(c.got)-1].Snapshot
	if got := member(t, first, "title"); got != "Mail (3)" {
		t.Fatalf("delivered title = %v, want Mail (3)", got)
	}
	raw, _ := json.Marshal(map[string]any{"mount": st.Mount, "title": "Mail (4)", "view": textView("base"),
		"intents": mountRecord("k", nil)["intents"], "assets": mountRecord("k", nil)["assets"]})
	up, err := DecodeUpdate(raw)
	wantOK(t, "decode update", err)
	st, err = r.Update("p", up)
	wantOK(t, "update", err)
	second := c.got[len(c.got)-1].Snapshot
	if got := member(t, second, "title"); got != "Mail (4)" {
		t.Fatalf("updated title = %v, want Mail (4)", got)
	}
	if first.Digest == "" || first.Digest == second.Digest {
		t.Fatalf("digest %q -> %q: a title change must change the digest", first.Digest, second.Digest)
	}
	raw, _ = json.Marshal(map[string]any{"mount": st.Mount, "view": textView("base")})
	up, err = DecodeUpdate(raw)
	wantOK(t, "decode update without title", err)
	_, err = r.Update("p", up)
	wantOK(t, "update without title", err)
	if got := member(t, c.got[len(c.got)-1].Snapshot, "title"); got != nil {
		t.Fatalf("title after an update without one = %v, want none", got)
	}
}

// pig additive (D614): the one intent a hidden mount takes is the one it declares whenHidden. Every other intent on a
// mount that is not live stays refused, and a stale reference stays stale.
func TestWhenHiddenIntentIsTheOnlyOneAHiddenMountTakes(t *testing.T) {
	a := richAbilities()
	open := func(extra map[string]any) map[string]any {
		d := map[string]any{"name": "fx.open", "arg": map[string]any{"type": "object"}, "whenHidden": true}
		maps.Copy(d, extra)
		return d
	}
	tap := map[string]any{"name": "fx.tap", "arg": map[string]any{"type": "object"}}
	wantOK(t, "one whenHidden intent", tryMount(t, a, mountRecord("k", map[string]any{"intents": []any{tap, open(nil)}})))
	for what, intents := range map[string][]any{
		"two whenHidden intents": {open(nil), open(map[string]any{"name": "fx.open2"})},
		"whenHidden with a node": {open(map[string]any{"nodeKey": "n-base"})},
		"arg that refuses {}":    {open(map[string]any{"arg": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []any{"value"}}})},
		"scalar arg":             {open(map[string]any{"arg": map[string]any{"type": "string"}})},
		"whenHidden not a bool":  {open(map[string]any{"whenHidden": "listed"})},
	} {
		if err := tryMount(t, a, mountRecord("k", map[string]any{"intents": intents})); err == nil || err.Code != CodeInvalid {
			t.Errorf("%s: error = %v, want invalid", what, err)
		}
	}

	r := New(Options{})
	if _, _, err := r.Attach(a, nil); err != nil {
		t.Fatal(err)
	}
	args, err := decodeRecord(t, mountRecord("k", map[string]any{"intents": []any{tap, open(nil)}}))
	wantOK(t, "decode", err)
	st, err := r.Mount("p", args)
	wantOK(t, "mount", err)
	_, err = r.Transition("p", "hide", RefArgs{Mount: st.Mount})
	wantOK(t, "hide", err)
	// The handler shows the mount, as an extension does; the answer is done, not stale: a show keeps the revision.
	shows := &fakeInvoker{invoke: func(_ context.Context, inv Invocation) (Result, error) {
		if _, err := r.Transition("p", "show", RefArgs{Mount: inv.Mount}); err != nil {
			return Result{Outcome: OutcomeFailed, Error: err.Error()}, nil
		}
		return Result{Outcome: OutcomeDone}, nil
	}}
	empty := json.RawMessage(`{}`)
	if res := r.Invoke(context.Background(), "p", st.Mount, "", "fx.tap", empty, shows); res.Outcome != OutcomeRefused || res.Error != "mount is hidden" {
		t.Fatalf("plain intent on a hidden mount = %+v, want refused: mount is hidden", res)
	}
	if res := r.Invoke(context.Background(), "p", Ref{Key: "k", Epoch: st.Mount.Epoch, Rev: st.Mount.Rev + 1}, "", "fx.open", empty, shows); res.Outcome != OutcomeStale {
		t.Fatalf("whenHidden intent with another rev = %+v, want stale", res)
	}
	if res := r.Invoke(context.Background(), "p", st.Mount, "", "fx.open", json.RawMessage(`{"x":1}`), shows); res.Outcome != OutcomeRefused {
		t.Fatalf("whenHidden intent with an argument outside its shape = %+v, want refused", res)
	}
	// The size bound of every intent, the client's limits.intentArgBytes, holds for this one too.
	big := json.RawMessage(`{` + strings.Repeat(" ", a.Limits.IntentArgBytes) + `}`)
	if res := r.Invoke(context.Background(), "p", st.Mount, "", "fx.open", big, shows); res.Outcome != OutcomeRefused || !strings.Contains(res.Error, "the limit is 256") {
		t.Fatalf("whenHidden intent with an argument of %d bytes = %+v, want refused by the limit of 256", len(big), res)
	}
	if now, _ := r.State("p", StateArgs{Key: "k"}); now.State != StateHidden {
		t.Fatalf("state after the refused intents = %s, want hidden", now.State)
	}
	if res := r.Invoke(context.Background(), "p", st.Mount, "", "fx.open", empty, shows); res.Outcome != OutcomeDone {
		t.Fatalf("whenHidden intent on a hidden mount = %+v, want done", res)
	}
	if now, _ := r.State("p", StateArgs{Key: "k"}); now.State != StateLive {
		t.Fatalf("state after the handler showed the mount = %s, want live", now.State)
	}
	if res := r.Invoke(context.Background(), "p", st.Mount, "", "fx.open", empty, shows); res.Outcome != OutcomeDone {
		t.Fatalf("whenHidden intent on a live mount = %+v, want done", res)
	}
	_, err = r.Transition("p", "remove", RefArgs{Mount: st.Mount})
	wantOK(t, "remove", err)
	if res := r.Invoke(context.Background(), "p", st.Mount, "", "fx.open", empty, shows); res.Outcome != OutcomeRefused || res.Error != "mount is removed" {
		t.Fatalf("whenHidden intent on a removed mount = %+v, want refused: mount is removed", res)
	}
}

// pig additive (D614): a focus record asks for the focus at mount or on the update that carries it, at most one entry
// asks, and the client's refusal is a notice the plugin reads.
func TestFocusRequestUpdateAndUnfocusedNotice(t *testing.T) {
	a := richAbilities()
	rec := func(focus ...any) map[string]any {
		m := mountRecord("k", map[string]any{"focus": focus})
		m["view"] = idView(4)
		return m
	}
	entry := func(node, request string) map[string]any { return map[string]any{"node": node, "request": request} }
	wantOK(t, "request update", tryMount(t, a, rec(entry("n0", "update"))))
	for what, m := range map[string]map[string]any{
		"mount and update":    rec(entry("n0", "mount"), entry("n1", "update")),
		"two update requests": rec(entry("n0", "update"), entry("n1", "update")),
		"unknown request":     rec(entry("n0", "updated")),
	} {
		if err := tryMount(t, a, m); err == nil || err.Code != CodeInvalid {
			t.Errorf("%s: error = %v, want invalid", what, err)
		}
	}

	r := New(Options{})
	c, att, _ := attach(t, r, a)
	args, err := decodeRecord(t, rec(entry("n2", "update")))
	wantOK(t, "decode", err)
	st, err := r.Mount("p", args)
	wantOK(t, "mount", err)
	focus, _ := member(t, c.got[len(c.got)-1].Snapshot, "focus").([]any)
	if len(focus) != 1 || focus[0].(map[string]any)["node"] != "n2" || focus[0].(map[string]any)["request"] != "update" {
		t.Fatalf("delivered focus = %v, want node n2 with request update", focus)
	}
	wantOK(t, "report unfocused", att.Report("p", st.Mount, Notice{Code: "unfocused", Severity: SeverityInfo, Path: "focus", Message: "the editor holds text"}))
	wantCode(t, att.Report("p", st.Mount, Notice{Code: "focused", Severity: SeverityInfo}), CodeInvalid)
	diag, err := r.Diagnostics("p", StateArgs{Key: "k"})
	wantOK(t, "diagnostics", err)
	if len(diag.Notices) != 1 || diag.Notices[0].Code != "unfocused" || diag.Notices[0].Path != "focus" || diag.Notices[0].Message != "the editor holds text" {
		t.Fatalf("notices = %+v, want one unfocused notice", diag.Notices)
	}
}
