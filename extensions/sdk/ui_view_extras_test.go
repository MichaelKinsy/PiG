package sdk

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The amendment members of D614 use the wire names the host's closed records expect, and the replicated claims decode
// into the four value kinds.
func TestViewAmendmentWire(t *testing.T) {
	yes := true
	half := 0.5
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"when", ViewWhen{Any: []ViewWhenAll{{All: []ViewClause{{Claim: "display.colors", Op: "atLeast", Value: 256}, {Claim: "display.hover", Op: "present"}, {Claim: "display.dark", Op: "equals", Value: false}}}}},
			`{"any":[{"all":[{"claim":"display.colors","op":"atLeast","value":256},{"claim":"display.hover","op":"present"},{"claim":"display.dark","op":"equals","value":false}]}]}`},
		{"drawing", ViewDrawing{Profile: "scene", Node: "n", Viewport: &ViewViewport{Width: 8, Height: 4, Fit: "contain"},
			Shapes:  []ViewDrawShape{{Kind: "group", Opacity: &half, Children: []ViewDrawShape{{Kind: "polyline", Points: []ViewPoint{{X: 1}, {Y: 2}}, Closed: true}, {Kind: "path", Commands: []ViewPathCommand{{Op: "move"}, {Op: "quad", X1: 1, Y1: 1, X: 2}}}}}},
			Regions: []ViewRegion{{Key: "r", Shape: ViewDrawShape{Kind: "ellipse", RX: 1, RY: 2}, Gestures: []string{"press"}, Intent: "x.go", Name: "Go"}}},
			`{"profile":"scene","node":"n","viewport":{"x":0,"y":0,"width":8,"height":4,"fit":"contain"},"shapes":[{"kind":"group","opacity":0.5,"children":[{"kind":"polyline","points":[{"x":1,"y":0},{"x":0,"y":2}],"closed":true},{"kind":"path","commands":[{"op":"move"},{"op":"quad","x":2,"x1":1,"y1":1}]}]}],"regions":[{"key":"r","shape":{"kind":"ellipse","rx":1,"ry":2},"gestures":["press"],"intent":"x.go","name":"Go"}]}`},
		{"pixels", ViewDrawing{Profile: "pixels", Pixels: &ViewPixels{Width: 2, Height: 3, Asset: "img", Scaling: "nearest"}},
			`{"profile":"pixels","pixels":{"width":2,"height":3,"asset":"img","scaling":"nearest"}}`},
		{"a11y", ViewA11y{Node: "n", Name: "N", Role: "button", ReadOnly: &yes, Relations: []ViewRelation{{Kind: "owns", Target: "m"}}},
			`{"node":"n","name":"N","role":"button","readOnly":true,"relations":[{"kind":"owns","target":"m"}]}`},
		{"focus", ViewFocus{Node: "n", Request: "mount", Restore: "previous"}, `{"node":"n","request":"mount","restore":"previous"}`},
		{"focus on update", ViewFocus{Node: "n", Request: "update"}, `{"node":"n","request":"update"}`},
		{"intent a hidden mount takes", ViewIntent{Name: "x.open", Arg: ViewShape{Type: "object"}, WhenHidden: true}, `{"name":"x.open","arg":{"type":"object"},"whenHidden":true}`},
		{"intent the user's dismissal raises", ViewIntent{Name: "x.close", Arg: ViewShape{Type: "object"}, Dismiss: true}, `{"name":"x.close","arg":{"type":"object"},"dismiss":true}`},
		{"key binding", ViewBinding{Key: "shift+left", Intent: "x.page", Arg: map[string]any{}, Name: "Back"}, `{"key":"shift+left","intent":"x.page","arg":{},"name":"Back"}`},
		{"gesture binding", ViewBinding{Gesture: "swipe-left", Intent: "x.page"}, `{"gesture":"swipe-left","intent":"x.page"}`},
	}
	for _, tc := range cases {
		got, err := json.Marshal(tc.in)
		if err != nil || string(got) != tc.want {
			t.Errorf("%s:\n got %s (%v)\nwant %s", tc.name, got, err, tc.want)
		}
	}

	var s viewState
	s.applyAbilities(json.RawMessage(`{"slots":["panel"],"claims":[{"name":"a.flag","value":true},{"name":"a.count","value":4242},{"name":"a.kind","value":"k"},{"name":"a.list","value":["x","y"]}],
		"slotClaims":[{"slot":"panel","claims":[{"name":"a.count","value":77}]}],"drawing":{"profiles":["scene"],"shapes":5,"pixels":16}}`))
	a := s.abilities
	if a == nil {
		t.Fatal("abilities with claims did not decode")
	}
	want := []ViewClaim{{"a.flag", true}, {"a.count", int64(4242)}, {"a.kind", "k"}, {"a.list", []string{"x", "y"}}}
	if !reflect.DeepEqual(a.Claims, want) || a.SlotClaims[0].Slot != ViewSlotPanel || a.SlotClaims[0].Claims[0].Value != int64(77) || a.Drawing == nil || a.Drawing.Shapes != 5 || a.Drawing.Pixels != 16 {
		t.Errorf("abilities = %+v", a)
	}
	// A claim outside the closed value subset makes the whole snapshot unusable rather than half-read.
	s.applyAbilities(json.RawMessage(`{"claims":[{"name":"a.b","value":1.5}]}`))
	if s.abilities != nil {
		t.Errorf("a fractional claim decoded: %+v", s.abilities)
	}

	var st ViewStatus
	if err := json.Unmarshal([]byte(`{"mount":{"key":"k","epoch":2,"rev":1},"state":"finished","result":{"picked":77}}`), &st); err != nil || string(st.Result) != `{"picked":77}` {
		t.Errorf("status result = %s (%v)", st.Result, err)
	}
	var n ViewNotice
	if err := json.Unmarshal([]byte(`{"code":"limit","severity":"error","path":"update","measured":6,"limit":5,"epoch":2,"rev":1,"count":3}`), &n); err != nil ||
		n.Code != "limit" || n.Measured == nil || *n.Measured != 6 || *n.Limit != 5 || n.Count != 3 {
		t.Errorf("notice = %+v (%v)", n, err)
	}
}
