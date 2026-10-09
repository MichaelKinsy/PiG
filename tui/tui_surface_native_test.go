package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// slotDock docks editor in a slot between two lines, as interactive mode
// docks its editor container, and reports the editor through the hooks.
func slotDock(t *testing.T) (*TuiSurface, *recordingSession, *Container, *Editor) {
	t.Helper()
	editor := NewEditor()
	slot := NewContainer(editor)
	surface, session := startedSurface(t, nil, NewContainer(NewText("status"), slot, NewText("footer")))
	surface.SetHooks(SurfaceHooks{Editor: func() *Editor { return editor }})
	surface.RepaintAll()
	return surface, session, slot, editor
}

// dockEntry returns the dock node whose id has prefix.
func dockEntry(t *testing.T, session *recordingSession, prefix string) (string, frontend.Node) {
	t.Helper()
	for _, entry := range session.tree[frontend.RegionDock] {
		if strings.HasPrefix(entry.id, prefix) {
			return entry.id, entry.node
		}
	}
	t.Fatalf("no dock node %q* in %q", prefix, dockIDs(session))
	return "", nil
}

// runAction steps a until it completes, handing each key to handle and
// rendering after it, as the input loop does, and returns the keys.
func runAction(surface *TuiSurface, action frontend.Action, handle func(string)) []string {
	a := NewNativeAction(action)
	var all []string
	for range 1000 {
		keys := surface.NativeKeys(a)
		if len(keys) == 0 {
			return all
		}
		for _, key := range keys {
			handle(key)
			surface.Render()
		}
		all = append(all, keys...)
	}
	panic("action did not finish")
}

// A selector in the editor's place arrives as a Selector node where the
// editor node was, between the lines above and below; a selection move
// updates only that node; a new selector gets a new id; and the editor
// node returns with the editor.
func TestNativeSelectorTakesTheEditorsPlace(t *testing.T) {
	surface, session, slot, editor := slotDock(t)
	selector := NewExtensionSelectorComponent("Pick one\nThe message", []string{"alpha", "beta", "gamma"}, func(string) {}, func() {})
	selector.SetDescription("More")
	slot.SetChildren(selector)
	surface.Render()
	if ids := dockIDs(session); !slices.Equal(ids, []string{"dock", "selector.1", "dock.below"}) {
		t.Fatalf("dock ids = %q", ids)
	}
	if above, below := dockText(session, "dock"), dockText(session, "dock.below"); !strings.Contains(above, "status") || !strings.Contains(below, "footer") || strings.Contains(above+below, "alpha") {
		t.Fatalf("above = %q, below = %q", above, below)
	}
	_, node := dockEntry(t, session, "selector.")
	want := frontend.Selector{
		Title:       "Pick one",
		Description: "The message\nMore",
		Items:       []frontend.SelectorItem{{ID: "0", Label: "alpha"}, {ID: "1", Label: "beta"}, {ID: "2", Label: "gamma"}},
		Selected:    "0",
	}
	if !selectorsEqual(node.(frontend.Selector), want) {
		t.Fatalf("selector = %#v", node)
	}

	frames := len(session.frames)
	selector.HandleInput("\x1b[B")
	surface.Render()
	ops := opsSince(session, frames)
	if len(ops) != 1 || ops[0].Kind != frontend.Update || ops[0].ID != "selector.1" || ops[0].Node.(frontend.Selector).Selected != "1" {
		t.Fatalf("ops for a move = %#v", ops)
	}

	slot.SetChildren(editor)
	surface.Render()
	if ids := dockIDs(session); !slices.Equal(ids, []string{"dock", "editor", "dock.below"}) {
		t.Fatalf("dock after the selector = %q", ids)
	}
	slot.SetChildren(NewExtensionSelectorComponent("Again", []string{"x"}, func(string) {}, func() {}))
	surface.Render()
	if ids := dockIDs(session); !slices.Equal(ids, []string{"dock", "selector.2", "dock.below"}) {
		t.Fatalf("dock with a second selector = %q", ids)
	}
	assertReplayIsFresh(t, surface, session)
}

// The settings frame's borders around a native settings list are not drawn,
// as the editor's are not; the lines that are not borders stay.
func TestNativeSettingsDropTheFrameBorders(t *testing.T) {
	surface, session, slot, _ := slotDock(t)
	list := NewSettingsList([]SettingItem{
		{ID: "a", Label: "Alpha", Description: "First", CurrentValue: "on", Values: []string{"on", "off"}},
		{ID: "b", Label: "Beta", CurrentValue: "x", Submenu: func(string, func(*string, *SubmenuDoneOptions)) Component { return NewText("sub") }},
		{ID: "c", Label: "Gamma", CurrentValue: "1", Submenu: func(string, func(*string, *SubmenuDoneOptions)) Component { return NewText("sub") }},
	}, 10, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: true})
	slot.SetChildren(NewContainer(NewDynamicBorder(), list, NewText("note"), NewDynamicBorder()))
	surface.Render()
	id, node := dockEntry(t, session, "selector.")
	if ids := dockIDs(session); !slices.Equal(ids, []string{"dock", id, "dock.below"}) {
		t.Fatalf("dock ids = %q", ids)
	}
	if text := dockText(session, "dock") + dockText(session, "dock.below"); strings.Contains(text, "─") || !strings.Contains(text, "note") {
		t.Fatalf("dock lines = %q", text)
	}
	want := frontend.Settings{
		Searchable: true,
		Items: []frontend.Setting{
			{ID: "a", Label: "Alpha", Description: "First", Value: "on", Values: []string{"on", "off"}},
			{ID: "b", Label: "Beta", Value: "x", Submenu: true},
			{ID: "c", Label: "Gamma", Value: "1", Submenu: true},
		},
		Selected: "a",
	}
	if !settingsEqual(node.(frontend.Settings), want) {
		t.Fatalf("settings = %#v", node)
	}
	// Outside a frame, a dynamic border is a component like any other.
	slot.SetChildren(NewEditor())
	surface.Render()
}

// A list that filters reports its query and the matching items, with ids
// that stay those of the unfiltered list; an empty result selects nothing.
func TestNativeFilterableListReportsItsFilter(t *testing.T) {
	surface, session, slot, _ := slotDock(t)
	list := NewFilterableList("Fork", []string{"apple pie", "banana", "apricot"})
	list.Descriptions = []string{"one", "", "three"}
	slot.SetChildren(list)
	surface.Render()
	for _, key := range []string{"a", "p"} {
		list.HandleInput(key)
	}
	surface.Render()
	_, node := dockEntry(t, session, "selector.")
	want := frontend.Selector{
		Title: "Fork", Searchable: true, Query: "ap",
		Items:    []frontend.SelectorItem{{ID: "0", Label: "apple pie", Detail: "one"}, {ID: "2", Label: "apricot", Detail: "three"}},
		Selected: "0",
	}
	if !selectorsEqual(node.(frontend.Selector), want) {
		t.Fatalf("filtered = %#v", node)
	}
	list.HandleInput("z")
	surface.Render()
	if _, node = dockEntry(t, session, "selector."); len(node.(frontend.Selector).Items) != 0 || node.(frontend.Selector).Selected != "" {
		t.Fatalf("empty result = %#v", node)
	}
}

// A submenu reports its items by value, its description and its search.
func TestNativeSelectSubmenuReportsItsItems(t *testing.T) {
	surface, session, slot, _ := slotDock(t)
	items := []SelectItem{{Value: "dark", Label: "Dark"}, {Value: "light", Label: "Light", Description: "bright"}}
	submenu := NewSelectSubmenu("Theme", "Pick a theme", items, "light", SelectSubmenuOptions{Searchable: true})
	slot.SetChildren(submenu)
	surface.Render()
	_, node := dockEntry(t, session, "selector.")
	want := frontend.Selector{
		Title: "Theme", Description: "Pick a theme", Searchable: true,
		Items:    []frontend.SelectorItem{{ID: "dark", Label: "Dark"}, {ID: "light", Label: "Light", Detail: "bright"}},
		Selected: "light",
	}
	if !selectorsEqual(node.(frontend.Selector), want) {
		t.Fatalf("submenu = %#v", node)
	}
}

// Highlight moves with the selection keys and never wraps around, from the
// first item to the last and back; the current item, an unknown item and a
// singleton list need no keys.
func TestNativeHighlightMovesWithoutWrapping(t *testing.T) {
	surface, _, slot, _ := slotDock(t)
	selector := NewExtensionSelectorComponent("Pick", []string{"a", "b", "c"}, func(string) {}, func() {})
	slot.SetChildren(selector)
	surface.Render()
	id := surface.nativeIDText
	act := func(item string) []string {
		return runAction(surface, frontend.Action{Node: id, Kind: frontend.Highlight, Item: item}, selector.HandleInput)
	}
	if keys := act("2"); !slices.Equal(keys, []string{"\x1b[B", "\x1b[B"}) || selector.cursor != 2 {
		t.Fatalf("to the last: keys %q, cursor %d", keys, selector.cursor)
	}
	if keys := act("0"); !slices.Equal(keys, []string{"\x1b[A", "\x1b[A"}) || selector.cursor != 0 {
		t.Fatalf("to the first: keys %q, cursor %d", keys, selector.cursor)
	}
	if keys := act("0"); keys != nil {
		t.Fatalf("to the current: keys %q", keys)
	}
	if keys := act("9"); keys != nil {
		t.Fatalf("to an unknown item: keys %q", keys)
	}
	if selector.Done() {
		t.Fatal("highlight chose")
	}

	single := NewExtensionSelectorComponent("One", []string{"only"}, func(string) {}, func() {})
	slot.SetChildren(single)
	surface.Render()
	if keys := runAction(surface, frontend.Action{Node: surface.nativeIDText, Kind: frontend.Highlight, Item: "0"}, single.HandleInput); keys != nil {
		t.Fatalf("singleton: keys %q", keys)
	}
}

// Choose moves and confirms, returning the option the keys select; with no
// item it confirms the selection; Dismiss cancels; an action on a node that
// is no longer shown does nothing.
func TestNativeChooseAndDismissActAsKeys(t *testing.T) {
	surface, _, slot, editor := slotDock(t)
	selector := NewExtensionSelectorComponent("Pick", []string{"a", "b", "c"}, func(string) {}, func() {})
	slot.SetChildren(selector)
	surface.Render()
	id := surface.nativeIDText
	handle := func(key string) {
		selector.HandleInput(key)
		if selector.Done() {
			slot.SetChildren(editor)
		}
	}
	keys := runAction(surface, frontend.Action{Node: id, Kind: frontend.Choose, Item: "1"}, handle)
	if !slices.Equal(keys, []string{"\x1b[B", "\r"}) || selector.SelectedValue() != "b" {
		t.Fatalf("choose: keys %q, value %q", keys, selector.SelectedValue())
	}
	if keys := runAction(surface, frontend.Action{Node: id, Kind: frontend.Choose}, handle); keys != nil {
		t.Fatalf("choose on a closed selector: keys %q", keys)
	}

	selector = NewExtensionSelectorComponent("Pick", []string{"a", "b"}, func(string) {}, func() {})
	slot.SetChildren(selector)
	surface.Render()
	if keys := runAction(surface, frontend.Action{Node: surface.nativeIDText, Kind: frontend.Choose}, handle); !slices.Equal(keys, []string{"\r"}) || selector.SelectedValue() != "a" {
		t.Fatalf("choose the selection: keys %q, value %q", keys, selector.SelectedValue())
	}
	selector = NewExtensionSelectorComponent("Pick", []string{"a", "b"}, func(string) {}, func() {})
	slot.SetChildren(selector)
	surface.Render()
	if keys := runAction(surface, frontend.Action{Node: "selector.1", Kind: frontend.Dismiss}, handle); keys != nil {
		t.Fatalf("dismiss with a stale id: keys %q", keys)
	}
	if keys := runAction(surface, frontend.Action{Node: surface.nativeIDText, Kind: frontend.Dismiss}, handle); !slices.Equal(keys, []string{"\x1b"}) || !selector.Cancelled() {
		t.Fatalf("dismiss: keys %q, cancelled %v", keys, selector.Cancelled())
	}
}

// Choose confirms only once its move landed: a key the user types between
// the move and the press, which moves the selection elsewhere, cancels the
// press instead of confirming the item the user moved to.
func TestNativeChooseDoesNotConfirmAfterAnInterveningMove(t *testing.T) {
	surface, _, slot, _ := slotDock(t)
	selector := NewExtensionSelectorComponent("Pick", []string{"a", "b", "c"}, func(string) {}, func() {})
	slot.SetChildren(selector)
	surface.Render()
	a := NewNativeAction(frontend.Action{Node: surface.nativeIDText, Kind: frontend.Choose, Item: "1"})
	keys := surface.NativeKeys(a)
	if !slices.Equal(keys, []string{"\x1b[B"}) {
		t.Fatalf("move keys = %q", keys)
	}
	selector.HandleInput(keys[0])
	selector.HandleInput("\x1b[B") // the user's own key, after the move
	surface.Render()
	if keys := surface.NativeKeys(a); keys != nil {
		t.Fatalf("press after an intervening move = %q, want none", keys)
	}
	if selector.Done() {
		t.Fatalf("selector confirmed %q", selector.SelectedValue())
	}
}

// Filter edits the query a key at a time: it types the missing end, erases
// what differs, and does nothing for a list that does not search. Highlight
// on an empty result does nothing.
func TestNativeFilterTypesAndErases(t *testing.T) {
	surface, _, slot, _ := slotDock(t)
	list := NewFilterableList("Fork", []string{"apple", "apricot", "banana"})
	slot.SetChildren(list)
	surface.Render()
	id := surface.nativeIDText
	filter := func(query string) []string {
		return runAction(surface, frontend.Action{Node: id, Kind: frontend.Filter, Value: query}, list.HandleInput)
	}
	if keys := filter("ap"); !slices.Equal(keys, []string{"a", "p"}) || list.FilterText() != "ap" {
		t.Fatalf("type: keys %q, filter %q", keys, list.FilterText())
	}
	if keys := filter("ab"); !slices.Equal(keys, []string{"\x7f", "b"}) || list.FilterText() != "ab" {
		t.Fatalf("change: keys %q, filter %q", keys, list.FilterText())
	}
	if keys := filter("zzz"); len(keys) != 5 || list.FilterText() != "zzz" {
		t.Fatalf("replace: keys %q, filter %q", keys, list.FilterText())
	}
	if keys := runAction(surface, frontend.Action{Node: id, Kind: frontend.Highlight, Item: "2"}, list.HandleInput); keys != nil {
		t.Fatalf("highlight in an empty result: keys %q", keys)
	}
	if keys := filter(""); len(keys) != 3 || list.FilterText() != "" {
		t.Fatalf("clear: keys %q, filter %q", keys, list.FilterText())
	}

	selector := NewExtensionSelectorComponent("Pick", []string{"a"}, func(string) {}, func() {})
	slot.SetChildren(selector)
	surface.Render()
	if keys := runAction(surface, frontend.Action{Node: surface.nativeIDText, Kind: frontend.Filter, Value: "a"}, selector.HandleInput); keys != nil {
		t.Fatalf("filter without search: keys %q", keys)
	}
}

// SetValue cycles a setting with the confirm key until it shows the value,
// through the host loop that applies each change; a value the setting does
// not have, a row that opens a selector, and the shown value need no keys.
// Choose opens a submenu row.
func TestNativeSetValueCyclesASetting(t *testing.T) {
	surface, _, slot, _ := slotDock(t)
	opened := ""
	list := NewSettingsList([]SettingItem{
		{ID: "mode", Label: "Mode", CurrentValue: "a", Values: []string{"a", "b", "c"}},
		{ID: "theme", Label: "Theme", CurrentValue: "dark", Values: []string{"dark", "light"}, Submenu: func(string, func(*string, *SubmenuDoneOptions)) Component {
			opened = "theme"
			return NewText("sub")
		}},
	}, 10, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: true})
	slot.SetChildren(list)
	surface.Render()
	id := surface.nativeIDText
	// handle is runModalSettingsList's loop: apply a change, then reset.
	handle := func(key string) {
		list.HandleInput(key)
		if list.Done() && !list.Cancelled() {
			changed, value := list.ChangedID, list.ChangedValue
			list.Reset()
			list.UpdateValue(changed, value)
		}
	}
	set := func(item, value string) []string {
		return runAction(surface, frontend.Action{Node: id, Kind: frontend.SetValue, Item: item, Value: value}, handle)
	}
	if keys := set("mode", "c"); !slices.Equal(keys, []string{"\r", "\r"}) || list.items[0].CurrentValue != "c" {
		t.Fatalf("cycle: keys %q, value %q", keys, list.items[0].CurrentValue)
	}
	if keys := set("mode", "a"); !slices.Equal(keys, []string{"\r"}) || list.items[0].CurrentValue != "a" {
		t.Fatalf("wrap: keys %q, value %q", keys, list.items[0].CurrentValue)
	}
	for _, args := range [][2]string{{"mode", "a"}, {"mode", "z"}, {"theme", "light"}, {"none", "a"}} {
		if keys := set(args[0], args[1]); keys != nil {
			t.Fatalf("set %q = %q: keys %q", args[0], args[1], keys)
		}
	}
	if keys := runAction(surface, frontend.Action{Node: id, Kind: frontend.Choose, Item: "theme"}, handle); !slices.Equal(keys, []string{"\x1b[B", "\r"}) || opened != "theme" {
		t.Fatalf("open: keys %q, opened %q", keys, opened)
	}
}

// tabbedSelector is a native component with views the tab key cycles.
type tabbedSelector struct {
	tabs []string
	tab  int
}

func (s *tabbedSelector) Render(int) []string { return []string{s.tabs[s.tab]} }
func (s *tabbedSelector) Invalidate()         {}
func (s *tabbedSelector) HandleInput(data string) {
	if data == "\t" {
		s.tab = (s.tab + 1) % len(s.tabs)
	}
}

func (s *tabbedSelector) NativeNode() (frontend.Node, bool) {
	node := frontend.Selector{Tab: s.tabs[s.tab]}
	for _, tab := range s.tabs {
		node.Tabs = append(node.Tabs, frontend.SelectorTab{ID: tab, Label: tab})
	}
	return node, true
}

// SwitchTab presses the tab key until the tab shows, through the last tab
// back to the first, and never for an unknown tab or the one showing.
func TestNativeSwitchTabPressesTheTabKey(t *testing.T) {
	surface, _, slot, _ := slotDock(t)
	selector := &tabbedSelector{tabs: []string{"current", "all", "named"}}
	slot.SetChildren(selector)
	surface.Render()
	id := surface.nativeIDText
	tab := func(item string) []string {
		return runAction(surface, frontend.Action{Node: id, Kind: frontend.SwitchTab, Item: item}, selector.HandleInput)
	}
	if keys := tab("named"); !slices.Equal(keys, []string{"\t", "\t"}) || selector.tab != 2 {
		t.Fatalf("forward: keys %q, tab %d", keys, selector.tab)
	}
	if keys := tab("current"); !slices.Equal(keys, []string{"\t"}) || selector.tab != 0 {
		t.Fatalf("around: keys %q, tab %d", keys, selector.tab)
	}
	if keys := tab("current"); keys != nil {
		t.Fatalf("showing: keys %q", keys)
	}
	if keys := tab("other"); keys != nil {
		t.Fatalf("unknown: keys %q", keys)
	}
}

// Overlays arrive in the overlay region with Pi's width and anchor. One
// that takes the keys turns the dock into lines and stops actions on the
// selector under it; one that does not leaves the dock as it is.
func TestNativeOverlaysArriveInTheirRegion(t *testing.T) {
	surface, session, slot, _ := slotDock(t)
	selector := NewExtensionSelectorComponent("Pick", []string{"a", "b"}, func(string) {}, func() {})
	slot.SetChildren(selector)
	surface.Render()
	id := surface.nativeIDText

	width := OverlayValue{Value: 20}
	passive := surface.ShowOverlay(NewText("hud"), OverlaySpec{Width: &width, Anchor: "top-right", NonCapturing: true}.Options())
	surface.Render()
	overlays := session.tree[frontend.RegionOverlay]
	if len(overlays) != 1 || !strings.HasPrefix(overlays[0].id, "overlay.") {
		t.Fatalf("overlay region = %#v", overlays)
	}
	if hud := overlays[0].node.(frontend.Overlay); hud.Width != 20 || hud.Anchor != "top-right" || !hud.NonCapturing || !strings.Contains(strings.Join(hud.Lines, ""), "hud") {
		t.Fatalf("hud = %#v", hud)
	}
	if ids := dockIDs(session); !slices.Equal(ids, []string{"dock", id, "dock.below"}) {
		t.Fatalf("dock under a passive overlay = %q", ids)
	}

	modal := surface.ShowOverlay(NewText("custom"), OverlayOptions{})
	surface.Render()
	overlays = session.tree[frontend.RegionOverlay]
	if len(overlays) != 2 || overlays[1].node.(frontend.Overlay).Anchor != "center" || overlays[1].node.(frontend.Overlay).NonCapturing {
		t.Fatalf("overlay region = %#v", overlays)
	}
	if ids := dockIDs(session); !slices.Equal(ids, []string{"dock"}) {
		t.Fatalf("dock under a capturing overlay = %q", ids)
	}
	if keys := runAction(surface, frontend.Action{Node: id, Kind: frontend.Choose}, selector.HandleInput); keys != nil {
		t.Fatalf("action under a capturing overlay: keys %q", keys)
	}
	modal.Close()
	passive.Close()
	surface.Render()
	if len(session.tree[frontend.RegionOverlay]) != 0 {
		t.Fatalf("overlays after closing = %#v", session.tree[frontend.RegionOverlay])
	}
	if _, node := dockEntry(t, session, "selector."); node.(frontend.Selector).Selected != "0" {
		t.Fatalf("selector after the overlays = %#v", node)
	}
	assertReplayIsFresh(t, surface, session)
}

// A forced repaint removes and reinserts the overlay region too.
func TestNativeResendReplacesOverlays(t *testing.T) {
	surface, session, _, _ := slotDock(t)
	surface.ShowOverlay(NewText("custom"), OverlayOptions{})
	surface.Render()
	frames := len(session.frames)
	surface.RepaintAll()
	var removed, inserted bool
	for _, op := range opsSince(session, frames) {
		if op.Region == frontend.RegionOverlay {
			removed = removed || op.Kind == frontend.Remove
			inserted = inserted || op.Kind == frontend.Insert
		}
	}
	if !removed || !inserted || len(session.tree[frontend.RegionOverlay]) != 1 {
		t.Fatalf("resend: removed %v, inserted %v, overlays %d", removed, inserted, len(session.tree[frontend.RegionOverlay]))
	}
}

// The equality checks see every field, so a change to any of them is sent.
func TestNativeNodesEqualSeesEveryField(t *testing.T) {
	base := frontend.Selector{
		Title: "t", Description: "d", Searchable: true, Query: "q", Selected: "1", Tab: "a", Status: "s", Loading: true, Confirm: "c",
		Items: []frontend.SelectorItem{{ID: "1", Label: "l", Detail: "x", Checked: true, Depth: 1}},
		Tabs:  []frontend.SelectorTab{{ID: "a", Label: "A"}},
	}
	changes := []func(*frontend.Selector){
		func(s *frontend.Selector) { s.Title = "" }, func(s *frontend.Selector) { s.Description = "" },
		func(s *frontend.Selector) { s.Searchable = false }, func(s *frontend.Selector) { s.Query = "" },
		func(s *frontend.Selector) { s.Selected = "" }, func(s *frontend.Selector) { s.Tab = "" },
		func(s *frontend.Selector) { s.Status = "" }, func(s *frontend.Selector) { s.Loading = false },
		func(s *frontend.Selector) { s.Confirm = "" }, func(s *frontend.Selector) { s.Items[0].Checked = false },
		func(s *frontend.Selector) { s.Items[0].Depth = 0 }, func(s *frontend.Selector) { s.Tabs[0].Label = "" },
	}
	for i, change := range changes {
		other := base
		other.Items = slices.Clone(base.Items)
		other.Tabs = slices.Clone(base.Tabs)
		change(&other)
		if nodesEqual(base, other) {
			t.Fatalf("selector change %d not seen", i)
		}
	}
	setting := frontend.Settings{Items: []frontend.Setting{{ID: "a", Values: []string{"x"}}}}
	changed := frontend.Settings{Items: []frontend.Setting{{ID: "a", Values: []string{"y"}}}}
	if nodesEqual(setting, changed) || !nodesEqual(setting, setting) {
		t.Fatal("settings equality")
	}
	overlay := frontend.Overlay{Lines: []string{"a"}, Width: 2}
	if nodesEqual(overlay, frontend.Overlay{Lines: []string{"a"}, Width: 3}) || !nodesEqual(overlay, overlay) {
		t.Fatal("overlay equality")
	}
}

// newPlainLoader is a Loader with no TUI to render into and no color functions.
func newPlainLoader(message string) *Loader {
	plain := func(text string) string { return text }
	return NewLoader(nil, plain, plain, message, nil)
}
