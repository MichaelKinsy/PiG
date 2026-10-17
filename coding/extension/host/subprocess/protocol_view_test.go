package subprocess

import (
	"encoding/json"
	"testing"
)

// The spec's example view (docs/plan/extension-component-kit.md §2) decodes
// into the protocol types and encodes back to the same JSON, so every SDK
// that emits the documented field names reaches the host's fields.
func TestViewPayloadRoundTripsTheSpecExample(t *testing.T) {
	const example = `{"root":{"kind":"container","children":[` +
		`{"kind":"dynamic-border","color":"accent"},` +
		`{"kind":"text","text":"Pick a track","paddingX":1,"paddingY":0},` +
		`{"kind":"select-list","id":"tracks","items":[{"value":"t1","label":"Blue in Green","description":"Miles Davis"}],"maxVisible":10},` +
		`{"kind":"hstack","children":[{"kind":"truncated-text","stack":{"grow":1},"text":"a"},{"kind":"lines","content":["x"],"image":{"ref":"ab"},"progress":{"value":1.5,"max":3},"list":{"items":[{"label":"Blue in Green","detail":"Miles Davis","columns":["5:37"]}],"selectedIndex":0}}],"gap":1},` +
		`{"kind":"loader","message":"wait","indicator":{"frames":[]}},` +
		`{"kind":"spacer","lines":2},` +
		`{"kind":"dynamic-border","color":"accent"}]},` +
		`"focus":"tracks","theme":{"accent":"#e0a040"}}`
	var view ViewPayload
	if err := json.Unmarshal([]byte(example), &view); err != nil {
		t.Fatal(err)
	}
	children := view.Root.Children
	if got := children[2]; got.Kind != ViewKindSelectList || got.ID != "tracks" || *got.MaxVisible != 10 || got.Items[0].Description != "Miles Davis" {
		t.Fatalf("select-list = %+v", got)
	}
	if got := children[1]; *got.PaddingX != 1 || *got.PaddingY != 0 {
		t.Fatalf("an explicit zero padding must stay distinguishable from the default: %+v", got)
	}
	if frames := children[4].Indicator.Frames; frames == nil || len(*frames) != 0 {
		t.Fatalf("an empty indicator frame list must stay distinguishable from the default spinner: %v", frames)
	}
	if lines := children[3].Children[1]; lines.Image.Ref != "ab" || lines.Progress.Max != 3 || lines.List.Items[0].Columns[0] != "5:37" || *children[3].Children[0].Stack.Grow != 1 {
		t.Fatalf("hstack = %+v", children[3])
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != example {
		t.Fatalf("round trip:\n got %s\nwant %s", encoded, example)
	}
}

// A frame without lines is distinguishable from a frame with empty lines:
// the first carries an authoritative view, the second annotates zero rows.
func TestRemoteOverlayRenderPayloadKeepsAbsentLinesApart(t *testing.T) {
	var absent, empty RemoteOverlayRenderPayload
	if err := json.Unmarshal([]byte(`{"key":"k","view":{"root":{"kind":"text"}}}`), &absent); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"key":"k","lines":[],"view":{"root":{"kind":"text"}}}`), &empty); err != nil {
		t.Fatal(err)
	}
	if absent.Lines != nil || empty.Lines == nil || len(absent.View) == 0 {
		t.Fatalf("absent=%#v empty=%#v", absent, empty)
	}
}
