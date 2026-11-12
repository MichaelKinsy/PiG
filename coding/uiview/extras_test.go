package uiview

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// richAbilities is a client that states claims, draws both profiles and takes large mounts.
func richAbilities() ClientAbilities {
	a := *abilities()
	a.Limits = Limits{Nodes: 1024, Depth: 16, SnapshotBytes: 1 << 22, TextBytes: 256, Mounts: 8, IntentArgBytes: 256}
	a.Input.Pointer = true
	a.Claims = []Claim{
		{Name: "display.colors", Value: json.RawMessage(`256`)},
		{Name: "display.kind", Value: json.RawMessage(`"terminal"`)},
		{Name: "display.hover", Value: json.RawMessage(`true`)},
		{Name: "input.gestures", Value: json.RawMessage(`["activate","drag"]`)},
	}
	a.Drawing = &DrawingAbilities{Profiles: []string{ProfileScene, ProfilePixels}, Shapes: maxShapes, Pixels: maxPixels}
	return a
}

func textView(text string) map[string]any {
	return map[string]any{"root": map[string]any{"kind": "text", "id": "n-" + text, "text": text}}
}

// mountRecord is a panel mount with one declared intent and one declared asset, plus the given members.
func mountRecord(key string, extra map[string]any) map[string]any {
	m := map[string]any{"key": key, "slot": "panel", "view": textView("base"),
		"intents": []any{map[string]any{"name": "fx.tap", "arg": map[string]any{"type": "object"}}},
		"assets":  []any{map[string]any{"id": "img", "mime": "image/png", "size": 4, "sha256": strings.Repeat("a", 64), "delivery": "inline"}}}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func decodeRecord(t *testing.T, m map[string]any) (MountArgs, *Error) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return DecodeMount(raw)
}

// tryMount decodes and mounts a record for plugin "p" on a fresh registry with one rich client, and returns the error
// of whichever step refused it.
func tryMount(t *testing.T, a ClientAbilities, m map[string]any) *Error {
	t.Helper()
	r := New(Options{})
	if _, _, err := r.Attach(a, nil); err != nil {
		return err
	}
	args, err := decodeRecord(t, m)
	if err != nil {
		return err
	}
	_, err = r.Mount("p", args)
	return err
}

func wantOK(t *testing.T, what string, err *Error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func when(claim, op string, value any) map[string]any {
	c := map[string]any{"claim": claim, "op": op}
	if value != nil {
		c["value"] = value
	}
	return map[string]any{"any": []any{map[string]any{"all": []any{c}}}}
}

func TestClaimBounds(t *testing.T) {
	claims := func(n int) []Claim {
		out := make([]Claim, n)
		for i := range out {
			out[i] = Claim{Name: fmt.Sprintf("area.fact%d", i), Value: json.RawMessage(`true`)}
		}
		return out
	}
	attach := func(mut func(*ClientAbilities)) *Error {
		a := richAbilities()
		mut(&a)
		_, _, err := New(Options{}).Attach(a, nil)
		return err
	}
	wantOK(t, "64 claims", attach(func(a *ClientAbilities) { a.Claims = claims(maxClaims) }))
	wantCode(t, attach(func(a *ClientAbilities) { a.Claims = claims(maxClaims + 1) }), CodeInvalid)
	wantOK(t, "64 slot claims", attach(func(a *ClientAbilities) { a.SlotClaims = []SlotClaims{{Slot: SlotPanel, Claims: claims(maxClaims)}} }))
	wantCode(t, attach(func(a *ClientAbilities) {
		a.SlotClaims = []SlotClaims{{Slot: SlotPanel, Claims: claims(maxClaims + 1)}}
	}), CodeInvalid)
	wantCode(t, attach(func(a *ClientAbilities) { a.SlotClaims = []SlotClaims{{Slot: "nowhere"}} }), CodeInvalid)
	wantCode(t, attach(func(a *ClientAbilities) { a.SlotClaims = []SlotClaims{{Slot: SlotPanel}, {Slot: SlotPanel}} }), CodeInvalid)

	name := func(n int) string { return "a." + strings.Repeat("b", n-2) }
	one := func(name, value string) func(*ClientAbilities) {
		return func(a *ClientAbilities) { a.Claims = []Claim{{Name: name, Value: json.RawMessage(value)}} }
	}
	wantOK(t, "64-byte name", attach(one(name(maxClaimName), `true`)))
	wantCode(t, attach(one(name(maxClaimName+1), `true`)), CodeInvalid)
	wantOK(t, "256-byte string", attach(one("a.b", `"`+strings.Repeat("s", maxClaimString)+`"`)))
	wantCode(t, attach(one("a.b", `"`+strings.Repeat("s", maxClaimString+1)+`"`)), CodeInvalid)
	list := func(n int) string {
		items := make([]string, n)
		for i := range items {
			items[i] = fmt.Sprintf(`"v%d"`, i)
		}
		return "[" + strings.Join(items, ",") + "]"
	}
	wantOK(t, "32-string list", attach(one("a.b", list(maxClaimList))))
	wantCode(t, attach(one("a.b", list(maxClaimList+1))), CodeInvalid)
	wantOK(t, "largest integer", attach(one("a.b", `9007199254740991`)))
	for _, bad := range []string{`9007199254740992`, `1.5`, `-1`, `01`, `1e3`, `null`, `{"a":1}`, `[1]`, `[null]`, `["x","x"]`, ``} {
		wantCode(t, attach(one("a.b", bad)), CodeInvalid)
	}
	for _, bad := range []string{"", "a", "a.", ".a", "A.b", "a.B", "a b.c", "a..b", "1a.b", "aB.c"} {
		wantCode(t, attach(one(bad, `true`)), CodeInvalid)
	}
	wantOK(t, "camel fact", attach(one("area.someFact2.more", `true`)))
	wantCode(t, attach(func(a *ClientAbilities) {
		a.Claims = []Claim{{Name: "a.b", Value: json.RawMessage(`1`)}, {Name: "a.b", Value: json.RawMessage(`2`)}}
	}), CodeInvalid)
	wantCode(t, attach(func(a *ClientAbilities) { a.Drawing = &DrawingAbilities{Profiles: []string{"scene2d/v1"}} }), CodeInvalid)
	wantCode(t, attach(func(a *ClientAbilities) { a.Drawing = &DrawingAbilities{Shapes: maxShapes + 1} }), CodeInvalid)
	wantCode(t, attach(func(a *ClientAbilities) { a.Drawing = &DrawingAbilities{Pixels: maxPixels + 1} }), CodeInvalid)
}

func TestAlternativeBounds(t *testing.T) {
	alts := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = map[string]any{"when": when("display.hover", "present", nil), "view": textView("alt")}
		}
		return out
	}
	a := richAbilities()
	wantOK(t, "8 alternatives", tryMount(t, a, mountRecord("k", map[string]any{"alternatives": alts(maxAlternatives)})))
	wantCode(t, tryMount(t, a, mountRecord("k", map[string]any{"alternatives": alts(maxAlternatives + 1)})), CodeInvalid)

	clause := map[string]any{"claim": "display.hover", "op": "present"}
	shape := func(groups, clauses int) map[string]any {
		all := make([]any, clauses)
		for i := range all {
			all[i] = clause
		}
		list := make([]any, groups)
		for i := range list {
			list[i] = map[string]any{"all": all}
		}
		return mountRecord("k", map[string]any{"alternatives": []any{map[string]any{"when": map[string]any{"any": list}, "view": textView("alt")}}})
	}
	wantOK(t, "8 groups of 8 clauses", tryMount(t, a, shape(maxGroups, maxClauses)))
	wantCode(t, tryMount(t, a, shape(maxGroups+1, 1)), CodeInvalid)
	wantCode(t, tryMount(t, a, shape(1, maxClauses+1)), CodeInvalid)
	wantCode(t, tryMount(t, a, shape(0, 1)), CodeInvalid)
	wantCode(t, tryMount(t, a, shape(1, 0)), CodeInvalid)

	bad := func(w map[string]any, extra map[string]any) *Error {
		alt := map[string]any{"when": w, "view": textView("alt")}
		for k, v := range extra {
			alt[k] = v
		}
		return tryMount(t, a, mountRecord("k", map[string]any{"alternatives": []any{alt}}))
	}
	wantCode(t, bad(when("display.colors", "matches", "x"), nil), CodeInvalid)                          // unknown operator
	wantCode(t, bad(when("display.colors", "present", 1), nil), CodeInvalid)                            // present takes no value
	wantCode(t, bad(when("display.colors", "atLeast", "many"), nil), CodeInvalid)                       // atLeast takes an integer
	wantCode(t, bad(when("display.colors", "contains", []any{}), nil), CodeInvalid)                     // contains takes a list
	wantCode(t, bad(when("display.colors", "equals", []any{"a"}), nil), CodeInvalid)                    // equals takes a scalar
	wantCode(t, bad(when("Colors", "present", nil), nil), CodeInvalid)                                  // not a claim name
	wantCode(t, bad(when("display.colors", "present", nil), map[string]any{"version": 1}), CodeInvalid) // closed record
	wantCode(t, tryMount(t, a, mountRecord("k", map[string]any{"alternatives": []any{map[string]any{"when": when("display.hover", "present", nil), "view": map[string]any{}}}})), CodeInvalid)
}

// resolved attaches a client and returns the text of the view it is sent for the one mount of r.
func resolved(t *testing.T, r *Registry, a ClientAbilities) Snapshot {
	t.Helper()
	att, rs, err := r.Attach(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer att.Detach()
	if len(rs.Snapshots) != 1 {
		t.Fatalf("resync has %d snapshots", len(rs.Snapshots))
	}
	return rs.Snapshots[0]
}

func TestAlternativeSelectionIsPerClient(t *testing.T) {
	r := New(Options{})
	if _, _, err := r.Attach(richAbilities(), nil); err != nil {
		t.Fatal(err)
	}
	args, err := decodeRecord(t, mountRecord("k", map[string]any{"alternatives": []any{
		map[string]any{"when": when("display.colors", "atLeast", 1000), "view": textView("truecolor")},
		map[string]any{"when": when("input.gestures", "contains", []any{"drag", "activate"}), "view": textView("draggable")},
		map[string]any{"when": when("display.kind", "equals", "terminal"), "view": textView("terminal")},
	}}))
	wantOK(t, "decode", err)
	if _, err := r.Mount("p", args); err != nil {
		t.Fatal(err)
	}
	with := func(mut func(*ClientAbilities)) Snapshot {
		a := richAbilities()
		mut(&a)
		return resolved(t, r, a)
	}
	check := func(what string, s Snapshot, alt int, text string) {
		t.Helper()
		if s.Alternative != alt || !strings.Contains(string(s.View), text) || s.Alternatives != nil || s.Degraded || len(s.Digest) != 64 {
			t.Errorf("%s: alternative %d view %s degraded %v digest %q, want alternative %d with %q", what, s.Alternative, s.View, s.Degraded, s.Digest, alt, text)
		}
	}
	// The first alternative whose predicate holds wins; 256 colors are below 1000, so the gestures decide.
	check("as attached", with(func(*ClientAbilities) {}), 2, "draggable")
	check("more colors", with(func(a *ClientAbilities) { a.Claims[0].Value = json.RawMessage(`1000`) }), 1, "truecolor")
	check("no drag", with(func(a *ClientAbilities) { a.Claims[3].Value = json.RawMessage(`["activate"]`) }), 3, "terminal")
	check("no claims", with(func(a *ClientAbilities) { a.Claims = nil }), 0, "base")
	// A slot's own claim replaces the client's for mounts of that slot only.
	check("panel claims", with(func(a *ClientAbilities) {
		a.SlotClaims = []SlotClaims{{Slot: SlotPanel, Claims: []Claim{{Name: "display.colors", Value: json.RawMessage(`4096`)}}}}
	}), 1, "truecolor")
	check("status claims", with(func(a *ClientAbilities) {
		a.SlotClaims = []SlotClaims{{Slot: SlotStatus, Claims: []Claim{{Name: "display.colors", Value: json.RawMessage(`4096`)}}}}
	}), 2, "draggable")
	// equals compares kind and value: the string "256" is not the integer 256.
	if (Clause{Claim: "display.colors", Op: "equals", Value: json.RawMessage(`"256"`)}).holds(richAbilitiesClaims()) {
		t.Error(`equals "256" held for the integer 256`)
	}
	if !(Clause{Claim: "display.colors", Op: "equals", Value: json.RawMessage(`256`)}).holds(richAbilitiesClaims()) {
		t.Error("equals 256 did not hold")
	}
}

func richAbilitiesClaims() map[string]claimValue {
	a := richAbilities()
	return a.claimsFor(SlotPanel)
}

func shapes(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = map[string]any{"kind": "rect", "x": 1, "y": 1, "width": 2, "height": 2}
	}
	return out
}

func scene(members map[string]any) map[string]any {
	d := map[string]any{"profile": "scene", "shapes": shapes(1)}
	for k, v := range members {
		d[k] = v
	}
	return d
}

func nested(depth int) map[string]any {
	s := map[string]any{"kind": "rect", "width": 1, "height": 1}
	for range depth - 1 {
		s = map[string]any{"kind": "group", "children": []any{s}}
	}
	return s
}

func points(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = map[string]any{"x": i, "y": i}
	}
	return out
}

func TestDrawingBounds(t *testing.T) {
	a := richAbilities()
	draw := func(d map[string]any) *Error { return tryMount(t, a, mountRecord("k", map[string]any{"drawing": d})) }
	wantOK(t, "4096 shapes", draw(scene(map[string]any{"shapes": shapes(maxShapes)})))
	wantCode(t, draw(scene(map[string]any{"shapes": shapes(maxShapes + 1)})), CodeInvalid)
	wantOK(t, "nesting 8", draw(scene(map[string]any{"shapes": []any{nested(maxShapeNest)}})))
	wantCode(t, draw(scene(map[string]any{"shapes": []any{nested(maxShapeNest + 1)}})), CodeInvalid)
	poly := func(n int) map[string]any {
		return scene(map[string]any{"shapes": []any{map[string]any{"kind": "polyline", "points": points(n)}}})
	}
	wantOK(t, "1024 points", draw(poly(maxShapePoints)))
	wantCode(t, draw(poly(maxShapePoints+1)), CodeInvalid)
	wantCode(t, draw(poly(1)), CodeInvalid)
	path := func(n int) map[string]any {
		cmds := make([]any, n)
		for i := range cmds {
			cmds[i] = map[string]any{"op": "line", "x": i, "y": i}
		}
		if n > 0 {
			cmds[0] = map[string]any{"op": "move", "x": 0, "y": 0}
		}
		return scene(map[string]any{"shapes": []any{map[string]any{"kind": "path", "commands": cmds}}})
	}
	wantOK(t, "1024 commands", draw(path(maxShapePoints)))
	wantCode(t, draw(path(maxShapePoints+1)), CodeInvalid)
	region := func(i int) map[string]any {
		return map[string]any{"key": fmt.Sprintf("r%d", i), "shape": map[string]any{"kind": "rect", "width": 4, "height": 4}, "gestures": []any{"activate"}, "intent": "fx.tap", "name": "Tap"}
	}
	regions := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = region(i)
		}
		return out
	}
	wantOK(t, "64 regions", draw(scene(map[string]any{"regions": regions(maxRegions)})))
	wantCode(t, draw(scene(map[string]any{"regions": regions(maxRegions + 1)})), CodeInvalid)
	pixels := func(w, h int) map[string]any {
		return map[string]any{"profile": "pixels", "pixels": map[string]any{"width": w, "height": h, "asset": "img", "scaling": "nearest"}}
	}
	wantOK(t, "2^20 pixels", draw(pixels(1024, 1024)))
	wantCode(t, draw(pixels(1024, 1025)), CodeInvalid)
	wantCode(t, draw(pixels(0, 4)), CodeInvalid)

	one := func(shape map[string]any) map[string]any { return scene(map[string]any{"shapes": []any{shape}}) }
	withRegion := func(mut func(map[string]any)) map[string]any {
		g := region(0)
		mut(g)
		return scene(map[string]any{"regions": []any{g}})
	}
	for name, d := range map[string]map[string]any{
		"versioned profile":        {"profile": "scene2d/v1", "shapes": shapes(1)},
		"scene without shapes":     {"profile": "scene"},
		"scene with pixels":        scene(map[string]any{"pixels": map[string]any{"width": 1, "height": 1, "asset": "img", "scaling": "nearest"}}),
		"unknown member":           scene(map[string]any{"payload": map[string]any{}}),
		"unknown kind":             one(map[string]any{"kind": "arc"}),
		"member of another kind":   one(map[string]any{"kind": "rect", "width": 1, "height": 1, "points": points(2)}),
		"bad color":                one(map[string]any{"kind": "rect", "width": 1, "height": 1, "fill": "#12345"}),
		"opacity above one":        one(map[string]any{"kind": "rect", "width": 1, "height": 1, "opacity": 1.5}),
		"transform of five":        one(map[string]any{"kind": "rect", "width": 1, "height": 1, "transform": []any{1, 0, 0, 1, 0}}),
		"path not from move":       one(map[string]any{"kind": "path", "commands": []any{map[string]any{"op": "line", "x": 1}}}),
		"undeclared image asset":   one(map[string]any{"kind": "image", "width": 1, "height": 1, "asset": "nope"}),
		"undeclared pixel asset":   {"profile": "pixels", "pixels": map[string]any{"width": 1, "height": 1, "asset": "nope", "scaling": "nearest"}},
		"unknown scaling":          {"profile": "pixels", "pixels": map[string]any{"width": 1, "height": 1, "asset": "img", "scaling": "cubic"}},
		"viewport without fit":     scene(map[string]any{"viewport": map[string]any{"x": 0, "y": 0, "width": 4, "height": 4}}),
		"drawing node not in view": scene(map[string]any{"node": "missing"}),
		"region intent undeclared": withRegion(func(g map[string]any) { g["intent"] = "fx.other" }),
		"region open polyline":     withRegion(func(g map[string]any) { g["shape"] = map[string]any{"kind": "polyline", "points": points(3)} }),
		"region unknown gesture":   withRegion(func(g map[string]any) { g["gestures"] = []any{"hover"} }),
		"region without gesture":   withRegion(func(g map[string]any) { g["gestures"] = []any{} }),
		"region without name":      withRegion(func(g map[string]any) { g["name"] = "" }),
	} {
		if err := draw(d); err == nil || err.Code != CodeInvalid {
			t.Errorf("%s: error = %v, want invalid", name, err)
		}
	}
	wantOK(t, "full scene", draw(scene(map[string]any{"node": "n-base", "viewport": map[string]any{"x": 0, "y": 0, "width": 8, "height": 8, "fit": "contain"}, "shapes": []any{
		map[string]any{"kind": "group", "key": "g", "opacity": 0.5, "transform": []any{1, 0, 0, 1, 2, 2}, "children": []any{
			map[string]any{"kind": "ellipse", "cx": 1, "cy": 1, "rx": 1, "ry": 1, "fill": "#ff000080", "stroke": map[string]any{"color": "accent", "width": 1}},
			map[string]any{"kind": "line", "x1": 0, "y1": 0, "x2": 4, "y2": 4},
			map[string]any{"kind": "text", "x": 1, "y": 1, "text": "hi", "size": 2, "align": "center"},
			map[string]any{"kind": "image", "x": 0, "y": 0, "width": 2, "height": 2, "asset": "img"},
			map[string]any{"kind": "path", "commands": []any{map[string]any{"op": "move", "x": 0, "y": 0}, map[string]any{"op": "cubic", "x1": 1, "y1": 1, "x2": 2, "y2": 2, "x": 3, "y": 3}, map[string]any{"op": "close"}}},
		}},
	}})))

	// What the client draws bounds a drawing too: no profile is unsupported, more than its shapes or pixels a limit.
	none := richAbilities()
	none.Drawing = nil
	wantCode(t, tryMount(t, none, mountRecord("k", map[string]any{"drawing": scene(nil)})), CodeUnsupported)
	few := richAbilities()
	few.Drawing = &DrawingAbilities{Profiles: []string{ProfileScene}, Shapes: 3, Pixels: 16}
	wantOK(t, "3 shapes of 3", tryMount(t, few, mountRecord("k", map[string]any{"drawing": scene(map[string]any{"shapes": shapes(3)})})))
	wantCode(t, tryMount(t, few, mountRecord("k", map[string]any{"drawing": scene(map[string]any{"shapes": shapes(4)})})), CodeLimit)
	wantCode(t, tryMount(t, few, mountRecord("k", map[string]any{"drawing": pixels(2, 2)})), CodeUnsupported)
	few.Drawing.Profiles = []string{ProfilePixels}
	wantOK(t, "16 pixels of 16", tryMount(t, few, mountRecord("k", map[string]any{"drawing": pixels(4, 4)})))
	wantCode(t, tryMount(t, few, mountRecord("k", map[string]any{"drawing": pixels(4, 5)})), CodeLimit)
	blind := richAbilities()
	blind.Input = Input{Text: true}
	wantCode(t, tryMount(t, blind, mountRecord("k", map[string]any{"drawing": scene(map[string]any{"regions": regions(1)})})), CodeUnsupported)
}

func TestFinishResult(t *testing.T) {
	r := New(Options{})
	var got []Snapshot
	if _, _, err := r.Attach(richAbilities(), func(d Delivery) bool { got = append(got, d.Snapshot); return true }); err != nil {
		t.Fatal(err)
	}
	finish := func(ref Ref, result string) (FinishArgs, *Error) {
		raw := fmt.Sprintf(`{"mount":{"key":%q,"epoch":%d,"rev":%d}`, ref.Key, ref.Epoch, ref.Rev)
		if result != "" {
			raw += `,"result":` + result
		}
		return DecodeFinish([]byte(raw + "}"))
	}
	st, _ := r.Mount("p", mountArgs(t, "k", "one"))
	if _, err := finish(st.Mount, `"`+strings.Repeat("r", maxResultBytes-1)+`"`); err == nil || err.Code != CodeInvalid {
		t.Fatalf("a 65537-byte result: %v, want invalid", err)
	}
	big := `"` + strings.Repeat("r", maxResultBytes-2) + `"`
	a, err := finish(st.Mount, big)
	wantOK(t, "a 65536-byte result", err)
	done, err := r.Finish("p", a)
	wantOK(t, "finish", err)
	if done.State != StateFinished || string(done.Result) != big {
		t.Fatalf("finish = %s with %d result bytes", done.State, len(done.Result))
	}
	if cur, _ := r.State("p", StateArgs{Key: "k"}); string(cur.Result) != big {
		t.Error("state does not carry the result")
	}
	if last := got[len(got)-1]; last.State != StateFinished || string(last.Result) != big {
		t.Error("the client's snapshot does not carry the result")
	}
	// The first terminal state wins: a second finish does not replace the result.
	a2, _ := finish(st.Mount, `{"choice":"no"}`)
	if cur, err := r.Finish("p", a2); err == nil || err.Code != CodeTerminal || string(cur.Result) != big {
		t.Errorf("second finish = %v", err)
	}
	// A finish without a result, and the other transitions, carry none.
	st2, _ := r.Mount("p", mountArgs(t, "k2", "two"))
	a3, err := finish(st2.Mount, "")
	wantOK(t, "finish without a result", err)
	if cur, _ := r.Finish("p", a3); cur.Result != nil {
		t.Errorf("result = %s, want none", cur.Result)
	}
	if _, err := DecodeFinish([]byte(`{"mount":{"key":"k","epoch":1,"rev":1},"result":1,"extra":2}`)); err == nil || err.Code != CodeInvalid {
		t.Errorf("unknown member: %v", err)
	}
}

func TestNoticesAreCoalescedBoundedAndRedacted(t *testing.T) {
	r := New(Options{})
	att, _, err := r.Attach(richAbilities(), nil)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := r.Mount("p", mountArgs(t, "k", "one"))
	notices := func() []Notice {
		d, err := r.Diagnostics("p", StateArgs{Key: "k"})
		if err != nil {
			t.Fatal(err)
		}
		return d.Notices
	}
	if n := notices(); len(n) != 0 {
		t.Fatalf("a fresh mount has notices: %+v", n)
	}
	// A rejected update leaves one notice; the same rejection again raises its count.
	stale := st.Mount
	stale.Rev = 7
	for range 3 {
		_, _ = r.Update("p", updateArgs(t, stale, "late"))
	}
	n := notices()
	if len(n) != 1 || n[0].Code != "stale" || n[0].Severity != SeverityError || n[0].Path != "update" || n[0].Count != 3 || n[0].Epoch != st.Mount.Epoch || n[0].Rev != 1 {
		t.Fatalf("after three stale updates: %+v", n)
	}
	// A limit the host enforced reports what it measured against what.
	_, _ = r.Update("p", updateArgs(t, st.Mount, strings.Repeat("x", 300)))
	if n = notices(); len(n) != 2 || n[1].Code != "limit" || !strings.Contains(n[1].Message, "300") {
		t.Fatalf("after an oversized update: %+v", n)
	}
	// A client's report is redacted: control characters go and the message is cut on a rune boundary.
	dirty := "cannot draw\x1b[31m \u0007" + strings.Repeat("\u00e9", 200)
	wantOK(t, "report", att.Report("p", st.Mount, Notice{Code: NoticeUndrawn, Severity: SeverityWarning, Message: dirty, Path: "root/0", Claim: "display.colors"}))
	n = notices()
	msg := n[2].Message
	if n[2].Code != NoticeUndrawn || len(msg) > maxNoticeMessage || len(msg) < maxNoticeMessage-1 || strings.ContainsAny(msg, "\x1b\u0007") || !strings.HasPrefix(msg, "cannot draw[31m ") || !strings.HasSuffix(msg, "\u00e9") {
		t.Fatalf("redacted notice = %q (%d bytes)", msg, len(msg))
	}
	wantCode(t, att.Report("p", st.Mount, Notice{Code: "panic", Severity: SeverityWarning}), CodeInvalid)
	wantCode(t, att.Report("p", st.Mount, Notice{Code: NoticeUndrawn, Severity: "fatal"}), CodeInvalid)
	wantCode(t, att.Report("p", Ref{Key: "k", Epoch: 99}, Notice{Code: NoticeUndrawn, Severity: SeverityInfo}), CodeStale)
	wantCode(t, att.Report("p", Ref{Key: "none"}, Notice{Code: NoticeUndrawn, Severity: SeverityInfo}), CodeNotFound)
	// 32 notices are kept; the 33rd distinct one drops the oldest.
	for i := len(notices()); i < maxNotices; i++ {
		wantOK(t, "report", att.Report("p", st.Mount, Notice{Code: NoticeUndrawn, Severity: SeverityInfo, Path: fmt.Sprintf("node/%d", i)}))
	}
	if n = notices(); len(n) != maxNotices || n[0].Code != "stale" {
		t.Fatalf("at the bound: %d notices, first %q", len(n), n[0].Code)
	}
	wantOK(t, "report", att.Report("p", st.Mount, Notice{Code: NoticeUndrawn, Severity: SeverityInfo, Path: "node/one-more"}))
	if n = notices(); len(n) != maxNotices || n[0].Code != "limit" || n[maxNotices-1].Path != "node/one-more" {
		t.Fatalf("past the bound: %d notices, first %q, last %q", len(n), n[0].Code, n[maxNotices-1].Path)
	}
	// An intent the host refused is noticed with its name; a new incarnation of the key starts empty.
	_, _ = r.Transition("p", "remove", RefArgs{Mount: st.Mount})
	st2, _ := r.Mount("p", mountArgs(t, "k", "again", IntentDecl{Name: "fx.add", Arg: Shape{Type: "object"}}))
	if n = notices(); len(n) != 0 {
		t.Fatalf("a remounted key kept notices: %+v", n)
	}
	if res := r.Invoke(t.Context(), "p", st2.Mount, "", "fx.missing", json.RawMessage(`{}`), &fakeInvoker{}); res.Outcome != OutcomeRefused {
		t.Fatalf("unknown intent = %+v", res)
	}
	if n = notices(); len(n) != 1 || n[0].Code != "refused" || n[0].Intent != "fx.missing" || n[0].Epoch != st2.Mount.Epoch {
		t.Fatalf("after a refused intent: %+v", n)
	}
	if _, err := r.Diagnostics("p", StateArgs{Key: "none"}); err == nil || err.Code != CodeNotFound {
		t.Errorf("diagnostics of no mount: %v", err)
	}
}

func idView(n int) map[string]any {
	children := make([]any, n)
	for i := range children {
		children[i] = map[string]any{"kind": "text", "id": fmt.Sprintf("n%d", i), "text": "t"}
	}
	return map[string]any{"root": map[string]any{"kind": "box", "children": children}}
}

func TestA11yAndFocusBounds(t *testing.T) {
	a := richAbilities()
	entries := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = map[string]any{"node": fmt.Sprintf("n%d", i), "name": "Item", "role": "listitem"}
		}
		return out
	}
	focus := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = map[string]any{"node": fmt.Sprintf("n%d", i)}
		}
		return out
	}
	rec := func(nodes int, extra map[string]any) map[string]any {
		m := mountRecord("k", extra)
		m["view"] = idView(nodes)
		return m
	}
	wantOK(t, "256 a11y entries", tryMount(t, a, rec(maxA11y+1, map[string]any{"a11y": entries(maxA11y)})))
	wantCode(t, tryMount(t, a, rec(maxA11y+1, map[string]any{"a11y": entries(maxA11y + 1)})), CodeInvalid)
	wantOK(t, "256 focus entries", tryMount(t, a, rec(maxA11y+1, map[string]any{"focus": focus(maxA11y)})))
	wantCode(t, tryMount(t, a, rec(maxA11y+1, map[string]any{"focus": focus(maxA11y + 1)})), CodeInvalid)
	relations := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = map[string]any{"kind": "controls", "target": fmt.Sprintf("n%d", i+1)}
		}
		return out
	}
	wantOK(t, "16 relations", tryMount(t, a, rec(32, map[string]any{"a11y": []any{map[string]any{"node": "n0", "relations": relations(maxRelations)}}})))
	wantCode(t, tryMount(t, a, rec(32, map[string]any{"a11y": []any{map[string]any{"node": "n0", "relations": relations(maxRelations + 1)}}})), CodeInvalid)
	name := func(n int) map[string]any {
		return rec(2, map[string]any{"a11y": []any{map[string]any{"node": "n0", "name": strings.Repeat("n", n)}}})
	}
	wantOK(t, "256-byte name", tryMount(t, a, name(maxA11yName)))
	wantCode(t, tryMount(t, a, name(maxA11yName+1)), CodeInvalid)
	for what, extra := range map[string]map[string]any{
		"unknown role":           {"a11y": []any{map[string]any{"node": "n0", "role": "wizard"}}},
		"node not in view":       {"a11y": []any{map[string]any{"node": "nx"}}},
		"node twice":             {"a11y": []any{map[string]any{"node": "n0"}, map[string]any{"node": "n0"}}},
		"relation to itself":     {"a11y": []any{map[string]any{"node": "n0", "relations": []any{map[string]any{"kind": "owns", "target": "n0"}}}}},
		"relation to no node":    {"a11y": []any{map[string]any{"node": "n0", "relations": []any{map[string]any{"kind": "owns", "target": "nx"}}}}},
		"unknown relation":       {"a11y": []any{map[string]any{"node": "n0", "relations": []any{map[string]any{"kind": "follows", "target": "n1"}}}}},
		"unknown a11y member":    {"a11y": []any{map[string]any{"node": "n0", "level": 2}}},
		"focus node not in view": {"focus": []any{map[string]any{"node": "nx"}}},
		"focus node twice":       {"focus": []any{map[string]any{"node": "n0"}, map[string]any{"node": "n0"}}},
		"two focus requests":     {"focus": []any{map[string]any{"node": "n0", "request": "mount"}, map[string]any{"node": "n1", "request": "mount"}}},
		"unknown request":        {"focus": []any{map[string]any{"node": "n0", "request": "now"}}},
		"unknown restore":        {"focus": []any{map[string]any{"node": "n0", "restore": "next"}}},
	} {
		if err := tryMount(t, a, rec(4, extra)); err == nil || err.Code != CodeInvalid {
			t.Errorf("%s: error = %v, want invalid", what, err)
		}
	}

	// A client is sent the entries of the nodes its view has: an entry for a node of an alternative it does not draw,
	// and a relation to such a node, stay behind.
	r := New(Options{})
	if _, _, err := r.Attach(richAbilities(), nil); err != nil {
		t.Fatal(err)
	}
	yes := true
	m := rec(2, map[string]any{
		"alternatives": []any{map[string]any{"when": when("display.colors", "atLeast", 1000), "view": textView("rich")}},
		"a11y": []any{
			map[string]any{"node": "n0", "name": "First", "role": "button", "checked": yes, "relations": []any{map[string]any{"kind": "controls", "target": "n1"}, map[string]any{"kind": "describedBy", "target": "n-rich"}}},
			map[string]any{"node": "n-rich", "name": "Rich"},
		},
		"focus": []any{map[string]any{"node": "n1", "request": "mount", "restore": "previous"}, map[string]any{"node": "n-rich"}},
	})
	args, err := decodeRecord(t, m)
	wantOK(t, "decode", err)
	if _, err := r.Mount("p", args); err != nil {
		t.Fatal(err)
	}
	base := resolved(t, r, richAbilities())
	if len(base.A11y) != 1 || base.A11y[0].Node != "n0" || base.A11y[0].Checked == nil || len(base.A11y[0].Relations) != 1 || base.A11y[0].Relations[0].Target != "n1" ||
		len(base.Focus) != 1 || base.Focus[0] != (Focus{Node: "n1", Request: "mount", Restore: "previous"}) {
		t.Errorf("base client: a11y %+v focus %+v", base.A11y, base.Focus)
	}
	richer := richAbilities()
	richer.Claims[0].Value = json.RawMessage(`4096`)
	alt := resolved(t, r, richer)
	if alt.Alternative != 1 || len(alt.A11y) != 1 || alt.A11y[0].Node != "n-rich" || len(alt.Focus) != 1 || alt.Focus[0].Node != "n-rich" {
		t.Errorf("alternative client: a11y %+v focus %+v", alt.A11y, alt.Focus)
	}
}
