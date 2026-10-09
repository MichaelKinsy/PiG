package subprocess

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

// kitProbeView is the conformance probe's tree (spec §10) with an override
// distinguishable from the default accent.
const kitProbeView = `{"root":{"kind":"container","children":[` +
	`{"kind":"dynamic-border","color":"accent"},` +
	`{"kind":"text","text":"Kit probe","paddingX":2,"paddingY":0,"bg":"customMessageBg"},` +
	`{"kind":"markdown","text":"- one\n- **two**","paddingX":1,"paddingY":0},` +
	`{"kind":"hstack","children":[{"kind":"truncated-text","stack":{"grow":1},"text":"left side"},{"kind":"truncated-text","stack":{"grow":1},"text":"right"}],"gap":1},` +
	`{"kind":"spacer","lines":1},` +
	`{"kind":"select-list","id":"kit-tracks","maxVisible":3,"selectedIndex":2,"items":[` +
	`{"value":"k0","label":"Track 0","description":"Artist 0"},{"value":"k1","label":"Track 1","description":"Artist 1"},` +
	`{"value":"k2","label":"Track 2","description":"Artist 2"},{"value":"k3","label":"Track 3","description":"Artist 3"},` +
	`{"value":"k4","label":"Track 4","description":"Artist 4"}]}]},` +
	`"focus":"kit-tracks","theme":{"accent":"#d75f00"}}`

// directProbe builds the same tree from the tui ports directly, as an
// in-process Pi extension would, with the coding-agent theme functions.
func directProbe(t *testing.T) (tui.Component, *tui.SelectList) {
	t.Helper()
	color, err := tui.ParseColor("#d75f00")
	if err != nil {
		t.Fatal(err)
	}
	th, err := tui.ActiveTheme().WithTokenColors(map[string]tui.Color{"accent": color})
	if err != nil {
		t.Fatal(err)
	}
	fg := func(token string) func(string) string { return func(s string) string { return th.Fg(token, s) } }
	md := tui.MarkdownThemeFor(th)
	items := make([]tui.SelectItem, 5)
	for i := range items {
		n := string(rune('0' + i))
		items[i] = tui.SelectItem{Value: "k" + n, Label: "Track " + n, Description: "Artist " + n}
	}
	list := tui.NewSelectList(items, 3, tui.SelectListThemeFor(th), tui.SelectListLayoutOptions{})
	list.SetSelectedIndex(2)
	one := 1
	return tui.NewContainer(
		tui.NewDynamicBorder(fg("accent")),
		tui.NewPaddedText("Kit probe", 2, 0, func(s string) string { return th.Bg("customMessageBg", s) }),
		tui.NewMarkdownWithOptions("- one\n- **two**", 1, 0, &md, nil, &tui.MarkdownOptions{}),
		tui.NewHStack([]tui.StackChild{
			{Component: tui.NewTruncatedText("left side", 0, 0), StackEntryOptions: tui.StackEntryOptions{Grow: &one}},
			{Component: tui.NewTruncatedText("right", 0, 0), StackEntryOptions: tui.StackEntryOptions{Grow: &one}},
		}, tui.StackOptions{Gap: &one}),
		tui.NewSpacer(1),
		list,
	), list
}

func TestViewKitRendersTheSameBytesAsTheTuiPorts(t *testing.T) {
	s := newViewSurface("k", newViewImageStore())
	if err := s.accept(json.RawMessage(kitProbeView), nil); err != nil {
		t.Fatal(err)
	}
	direct, _ := directProbe(t)
	for _, width := range []int{30, 72, 120} {
		got, want := s.Render(width), direct.Render(width)
		if !slices.Equal(got, want) {
			t.Fatalf("width %d:\n got %q\nwant %q", width, got, want)
		}
	}
	// The override is drawn: the default accent would not match.
	if def := tui.ActiveTheme().Fg("accent", "→ "); slices.ContainsFunc(s.Render(72), func(l string) bool { return len(l) >= len(def) && l[:len(def)] == def }) {
		t.Fatal("selected row drew the theme's accent, not the override")
	}
}

func TestViewKitRoutesBoundKeysToTheFocusedListAndReportsEvents(t *testing.T) {
	s := newViewSurface("k", newViewImageStore())
	var events []string
	s.sendEvent = func(ev ViewEventPayload) {
		item := ""
		if ev.Item != nil {
			item = ev.Item.Value
		}
		events = append(events, ev.Type+":"+string(rune('0'+ev.Index))+":"+item)
	}
	if err := s.accept(json.RawMessage(kitProbeView), nil); err != nil {
		t.Fatal(err)
	}
	direct, list := directProbe(t)
	down := "\x1b[B"
	for _, key := range []string{down, down, down} {
		if !s.HandleViewInput(key) {
			t.Fatalf("down was not taken by the focused list")
		}
		list.HandleInput(key)
		if got, want := s.Render(72), direct.Render(72); !slices.Equal(got, want) {
			t.Fatalf("after down:\n got %q\nwant %q", got, want)
		}
	}
	if s.HandleViewInput("x") {
		t.Fatal("x is not bound by a select list and must reach the extension")
	}
	if !s.HandleViewInput("\r") {
		t.Fatal("enter was not taken")
	}
	want := []string{"selectionChange:3:k3", "selectionChange:4:k4", "selectionChange:0:k0", "select:0:k0"}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %q, want %q", events, want)
	}
	// A repeated selectedIndex does not override the user's selection.
	if err := s.accept(json.RawMessage(kitProbeView+" "), nil); err != nil {
		t.Fatal(err)
	}
	if view := s.FrontendView(72); view == nil || view.Root.Children[5].Selected != 0 || view.Focus != "kit-tracks" {
		t.Fatalf("a resent frame reset the host-owned selection")
	}
}

func TestViewKitDropsAViewThatDoesNotReproduceItsLines(t *testing.T) {
	s := newViewSurface("", newViewImageStore())
	view := `{"root":{"kind":"text","text":"hello","paddingX":1,"paddingY":0},"width":20}`
	good := tui.NewPaddedText("hello", 1, 0, nil).Render(20)
	if err := s.accept(json.RawMessage(view), good); err != nil {
		t.Fatal(err)
	}
	if s.FrontendView(20) == nil {
		t.Fatal("a view that reproduces its lines was dropped")
	}
	bad := []string{"hello, but different"}
	if err := s.accept(json.RawMessage(view), bad); err != nil {
		t.Fatal(err)
	}
	if s.FrontendView(20) != nil {
		t.Fatal("a view that does not reproduce its lines survived")
	}
	if got := s.Render(20); !slices.Equal(got, bad) {
		t.Fatalf("the terminal must draw the frame's own lines, got %q", got)
	}
}

func TestViewKitRejectsInvalidViews(t *testing.T) {
	for name, view := range map[string]string{
		"unknown kind":        `{"root":{"kind":"widget"}}`,
		"list without id":     `{"root":{"kind":"select-list"}}`,
		"unknown token":       `{"root":{"kind":"dynamic-border","color":"nope"}}`,
		"bad override":        `{"root":{"kind":"spacer"},"theme":{"accent":"orange"}}`,
		"closed override":     `{"root":{"kind":"spacer"},"theme":{"mdHeading":"#ffffff"}}`,
		"missing image":       `{"root":{"kind":"image","ref":"00","mimeType":"image/png"}}`,
		"annotated, no width": ``,
		"list rows short":     `{"root":{"kind":"lines","content":["a","b"],"list":{"items":[{"label":"a"}],"selectedIndex":0}}}`,
		"list rows long":      `{"root":{"kind":"lines","content":["a"],"list":{"items":[{"label":"a"},{"label":"b"}],"selectedIndex":0}}}`,
		"list selects past":   `{"root":{"kind":"lines","content":["a"],"list":{"items":[{"label":"a"}],"selectedIndex":1}}}`,
		"list selects below":  `{"root":{"kind":"lines","content":["a"],"list":{"items":[{"label":"a"}],"selectedIndex":-2}}}`,
	} {
		s := newViewSurface("", newViewImageStore())
		var lines []string
		if view == "" {
			view, lines = `{"root":{"kind":"spacer"}}`, []string{""}
		}
		if err := s.accept(json.RawMessage(view), lines); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The frontend-only list annotation reaches the frontend node with one item
// per row, and the terminal still draws the rows.
func TestViewKitReportsALinesListToTheFrontend(t *testing.T) {
	s := newViewSurface("", newViewImageStore())
	rows := []string{"> Blue in Green  Miles Davis", "  So What        Miles Davis"}
	view := `{"root":{"kind":"lines","content":["> Blue in Green  Miles Davis","  So What        Miles Davis"],` +
		`"list":{"items":[{"label":"Blue in Green","detail":"Miles Davis","columns":["Kind of Blue","5:37"]},{"label":"So What"}],"selectedIndex":1}}}`
	if err := s.accept(json.RawMessage(view), nil); err != nil {
		t.Fatal(err)
	}
	if got := s.Render(40); !slices.Equal(got, rows) {
		t.Fatalf("terminal rows = %q, want %q", got, rows)
	}
	fv := s.FrontendView(40)
	if fv == nil || fv.Root.List == nil {
		t.Fatalf("frontend view = %+v, want the list", fv)
	}
	want := frontend.ViewList{Items: []frontend.ViewListItem{{Label: "Blue in Green", Detail: "Miles Davis", Columns: []string{"Kind of Blue", "5:37"}}, {Label: "So What"}}, Selected: 1}
	if !reflect.DeepEqual(*fv.Root.List, want) || fv.Root.Rows != 2 {
		t.Fatalf("list = %+v (rows %d), want %+v", *fv.Root.List, fv.Root.Rows, want)
	}
}
