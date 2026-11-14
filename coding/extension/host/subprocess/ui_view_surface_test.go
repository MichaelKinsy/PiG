package subprocess

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

const mountListView = `{"root":{"kind":"vstack","children":[{"kind":"text","text":"Pick one"},` +
	`{"kind":"select-list","id":"pick","maxVisible":4,"items":[{"value":"a","label":"Alpha"},{"value":"b","label":"Beta"}]}]}%s}`

func mountView(focus string) json.RawMessage {
	return json.RawMessage(strings.Replace(mountListView, "%s", focus, 1))
}

type mountProbe struct {
	acts     []MountActivation
	leaves   int
	repaints int
}

func (p *mountProbe) surface() *MountSurface {
	return NewMountSurface(MountSurfaceOptions{
		Repaint:  func() { p.repaints++ },
		Activate: func(a MountActivation) { p.acts = append(p.acts, a) },
		Leave:    func() { p.leaves++ },
	})
}

// pig additive (D614): a mount's view is drawn by the D107 view kit, and the D107 events of its nodes become
// activations: Enter on a list item names the node and the item's value, Escape gives the focus up.
func TestMountSurfaceDrawsTheViewAndMapsListEvents(t *testing.T) {
	var p mountProbe
	s := p.surface()
	defer s.Close()
	if lines := s.Render(40); len(lines) != 0 {
		t.Fatalf("a surface with no view drew %q", lines)
	}
	if err := s.Accept(mountView(`,"focus":"pick"`), ""); err != nil {
		t.Fatal(err)
	}
	drawn := strings.Join(s.Render(40), "\n")
	for _, want := range []string{"Pick one", "Alpha", "Beta"} {
		if !strings.Contains(drawn, want) {
			t.Fatalf("the view was drawn without %q:\n%s", want, drawn)
		}
	}
	if !s.Focusable() {
		t.Fatal("a view whose focus names a list is not focusable")
	}
	rev := s.Revision()
	if !s.HandleInput("\x1b[B") || !s.HandleInput("\r") {
		t.Fatal("the focused list did not take Down and Enter")
	}
	if len(p.acts) != 1 || p.acts[0].Node != "pick" || p.acts[0].Value == nil || *p.acts[0].Value != "b" || p.acts[0].Item != "" {
		t.Fatalf("activations = %+v, want one of node pick with value b", p.acts)
	}
	if s.Revision() == rev || p.repaints == 0 {
		t.Fatalf("a key the list took changed neither the revision (%d) nor asked for a repaint (%d)", s.Revision(), p.repaints)
	}
	if !s.HandleInput("\x1b") || p.leaves != 1 {
		t.Fatalf("Escape on the list: leaves = %d, want 1", p.leaves)
	}
	if s.HandleInput("x") {
		t.Fatal("a list took a key it does not bind")
	}
}

// pig additive (D614): a view that names no focus takes the client's (the node of a focus request, else the first focus
// record of the mount), an unchanged view is not built again when the client names another node, and a rejected view
// leaves the frame the surface had.
func TestMountSurfaceDefaultFocusAndRejectedView(t *testing.T) {
	var p mountProbe
	s := p.surface()
	defer s.Close()
	if err := s.Accept(mountView(""), ""); err != nil {
		t.Fatal(err)
	}
	if s.Focusable() {
		t.Fatal("a view with no focus and no default is focusable")
	}
	built := s.s.frame
	if err := s.Accept(mountView(""), "pick"); err != nil {
		t.Fatal(err)
	}
	if s.s.frame != built {
		t.Fatal("an unchanged view was built again for a new default focus")
	}
	if !s.Focusable() {
		t.Fatal("the unchanged view did not give its keys to the node the client named")
	}
	if err := s.Accept(mountView(""), "nope"); err != nil {
		t.Fatal(err)
	}
	if s.s.frame != built || s.Focusable() {
		t.Fatal("a default focus that names no node of the unchanged view left the view focusable, or built it again")
	}
	if err := s.Accept(mountView(` `), "pick"); err != nil {
		t.Fatal(err)
	}
	if !s.Focusable() {
		t.Fatal("the default focus names a list of the view, and the view is not focusable")
	}
	before := s.Render(40)
	if err := s.Accept(json.RawMessage(`{"root":{"kind":"select-list"}}`), ""); err == nil {
		t.Fatal("a list without an id was accepted")
	}
	if after := s.Render(40); !slices.Equal(before, after) {
		t.Fatalf("a rejected view changed the frame:\n%q\n%q", before, after)
	}
}

// pig additive (D614): an input draws its cursor only while the surface holds the focus, a submit is an activation with
// the text, and an edit is not one.
func TestMountSurfaceInputFocusAndSubmit(t *testing.T) {
	var p mountProbe
	s := p.surface()
	defer s.Close()
	if err := s.Accept(json.RawMessage(`{"root":{"kind":"input","id":"name"},"focus":"name"}`), ""); err != nil {
		t.Fatal(err)
	}
	unfocused := slices.Clone(s.Render(30))
	s.SetFocused(true)
	focused := slices.Clone(s.Render(30))
	if slices.Equal(unfocused, focused) {
		t.Fatalf("the input draws the same with and without the focus: %q", focused)
	}
	s.SetFocused(false)
	if again := s.Render(30); !slices.Equal(unfocused, again) {
		t.Fatalf("the input kept its focused drawing after the focus left:\n%q\n%q", unfocused, again)
	}
	s.SetFocused(true)
	for _, key := range []string{"h", "i"} {
		if !s.HandleInput(key) {
			t.Fatalf("the focused input did not take %q", key)
		}
	}
	if len(p.acts) != 0 {
		t.Fatalf("an edit was an activation: %+v", p.acts)
	}
	s.HandleInput("\r")
	if len(p.acts) != 1 || p.acts[0].Node != "name" || p.acts[0].Value == nil || *p.acts[0].Value != "hi" {
		t.Fatalf("activations = %+v, want one of node name with value hi", p.acts)
	}
}

// pig additive (D614): a click on a mouse-region is an activation without a value.
func TestMountSurfaceMouseRegionClick(t *testing.T) {
	var p mountProbe
	s := p.surface()
	defer s.Close()
	view := `{"root":{"kind":"mouse-region","id":"go","region":{},"children":[{"kind":"text","text":"Go"}]}}`
	if err := s.Accept(json.RawMessage(view), ""); err != nil {
		t.Fatal(err)
	}
	s.Render(20)
	for _, typ := range []string{"press", "release", "click"} {
		s.HandleMouse(extension.RemoteMouseEvent{Type: typ, Button: "left", X: 1, Y: 0, ScreenX: 1, ScreenY: 0, Width: 20, Height: 1})
	}
	if len(p.acts) != 1 || p.acts[0].Node != "go" || p.acts[0].Value != nil {
		t.Fatalf("activations = %+v, want one of node go without a value", p.acts)
	}
}

// pig additive (D614): the kinds a mount surface declares are kinds of the D107 vocabulary, without image.
func TestMountNodeKindsAreTheViewVocabulary(t *testing.T) {
	kinds := MountNodeKinds()
	if !slices.IsSorted(kinds) || len(slices.Compact(slices.Clone(kinds))) != len(kinds) {
		t.Fatalf("kinds are not sorted and distinct: %v", kinds)
	}
	for _, kind := range kinds {
		if !knownViewKind(kind) {
			t.Errorf("kind %q is declared and is not in the view vocabulary", kind)
		}
	}
	for _, want := range []string{ViewKindText, ViewKindSelectList, ViewKindSettingsList, ViewKindInput, ViewKindEditor, ViewKindMouseRegion, ViewKindScrollView} {
		if !slices.Contains(kinds, want) {
			t.Errorf("kind %q is not declared", want)
		}
	}
	// A mount's view carries the bytes of its images in its own list, and the surface draws them from there.
	if !slices.Contains(kinds, ViewKindImage) {
		t.Error("image is not declared; a mount's view carries its images and the surface draws them")
	}
}
