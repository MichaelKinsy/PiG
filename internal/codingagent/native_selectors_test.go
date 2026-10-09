package codingagent

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

// nativeDock docks component in a slot of a started surface, as the editor
// container holds it.
func nativeDock(t *testing.T, component tui.Component) (*tui.TuiSurface, *fakeFrontendSession) {
	t.Helper()
	session := &fakeFrontendSession{}
	surface := tui.NewTuiSurfaceWithSize(session, 80, 24, func(err error) { t.Fatalf("apply: %v", err) })
	surface.SetLayout(tui.NewContainer(), tui.NewContainer(component))
	surface.Start()
	t.Cleanup(surface.Stop)
	return surface, session
}

// nativeDockSelector returns the dock's selector id and node.
func nativeDockSelector(t *testing.T, session *fakeFrontendSession) (string, frontend.Node) {
	t.Helper()
	var ids []string
	for _, entry := range dockTree(t, session) {
		if strings.HasPrefix(entry.id, "selector.") {
			return entry.id, entry.node
		}
		ids = append(ids, entry.id)
	}
	t.Fatalf("no selector in the dock %q", ids)
	return "", nil
}

// runNativeAction steps an action until it completes, handing each key to
// handle and rendering after it, as the input loop does, and returns the keys.
func runNativeAction(t *testing.T, surface *tui.TuiSurface, action frontend.Action, handle func(string)) []string {
	t.Helper()
	a := tui.NewNativeAction(action)
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
	t.Fatal("action did not finish")
	return nil
}

func nativeSelectorNode(t *testing.T, c tui.NativeComponent) frontend.Selector {
	t.Helper()
	node, ok := c.NativeNode()
	if !ok {
		t.Fatal("no native node")
	}
	selector, ok := node.(frontend.Selector)
	if !ok {
		t.Fatalf("node = %#v, want a selector", node)
	}
	return selector
}

func nativeItemIDs(items []frontend.SelectorItem) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}

// nativeSessionFixture is a current folder with a named root, its fork and
// the active session, and an all scope with one more session.
func nativeSessionFixture(t *testing.T) *SessionSelectorComponent {
	t.Helper()
	now := time.Now()
	current := []SessionInfo{
		{Path: "/s/a.jsonl", ID: "a", Name: "Alpha", MessageCount: 4, Modified: now.Add(-3 * time.Hour), AllMessagesText: "alpha"},
		{Path: "/s/b.jsonl", ID: "b", ParentSessionPath: "/s/a.jsonl", FirstMessage: "fix\nthe bug", MessageCount: 2, Modified: now.Add(-2 * time.Hour), AllMessagesText: "fix the bug"},
		{Path: "/s/c.jsonl", ID: "c", FirstMessage: "hello", MessageCount: 1, Modified: now.Add(-5 * time.Hour), AllMessagesText: "hello"},
	}
	all := append(slices.Clone(current), SessionInfo{Path: "/s/d.jsonl", ID: "d", CWD: "/work/x", FirstMessage: "elsewhere", MessageCount: 7, Modified: now.Add(-50 * time.Hour), AllMessagesText: "elsewhere"})
	s := newLoadedSessionSelector(func() ([]SessionInfo, error) { return current, nil }, func() ([]SessionInfo, error) { return all, nil },
		func(string, string) error { return nil }, func(string) error { return nil }, "/s/c.jsonl", sessionSelectorInputBindings(t))
	t.Cleanup(s.close)
	return s
}

// The session list reports its threaded rows in key order with what Pi
// shows on them, the active session checked, its scope as a tab, the name
// filter and sort as status, and its search.
func TestNativeSessionSelectorReportsTheList(t *testing.T) {
	s := nativeSessionFixture(t)
	want := frontend.Selector{
		Title:      "Resume Session (Current Folder)",
		Searchable: true,
		Items: []frontend.SelectorItem{
			{ID: "/s/a.jsonl", Label: "Alpha", Detail: "4 3h"},
			{ID: "/s/b.jsonl", Label: "fix the bug", Detail: "2 2h", Depth: 1},
			{ID: "/s/c.jsonl", Label: "hello", Detail: "1 5h", Checked: true},
		},
		Selected: "/s/a.jsonl",
		Tabs:     []frontend.SelectorTab{{ID: "current", Label: "Current Folder"}, {ID: "all", Label: "All"}},
		Tab:      "current",
		Status:   "Name: All  Sort: Threaded",
	}
	if got := nativeSelectorNode(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("node = %#v\nwant %#v", got, want)
	}
}

// Keys change the node: a move, the search, the scope tab, sort, name
// filter and path toggles, the delete confirmation and its refusal for the
// active session, and rename mode, which the node does not model.
func TestNativeSessionSelectorFollowsItsKeys(t *testing.T) {
	s := nativeSessionFixture(t)
	s.HandleInput("\x1b[B")
	if got := nativeSelectorNode(t, s).Selected; got != "/s/b.jsonl" {
		t.Fatalf("selected after down = %q", got)
	}
	for _, key := range []string{"h", "e", "l"} {
		s.HandleInput(key)
	}
	if got := nativeSelectorNode(t, s); got.Query != "hel" || !slices.Equal(nativeItemIDs(got.Items), []string{"/s/c.jsonl"}) || got.Selected != "/s/c.jsonl" {
		t.Fatalf("filtered = %#v", got)
	}
	for range 3 {
		s.HandleInput("\x7f")
	}
	s.HandleInput("\t")
	if got := nativeSelectorNode(t, s); !got.Loading || got.Tab != "all" {
		t.Fatalf("all scope while it loads = %#v", got)
	}
	s.drainLoadUpdates()
	got := nativeSelectorNode(t, s)
	if got.Title != "Resume Session (All)" || got.Tab != "all" || got.Query != "" || len(got.Items) != 4 {
		t.Fatalf("all scope = %#v", got)
	}
	if d := got.Items[3]; d.ID != "/s/d.jsonl" || d.Detail != "/work/x 7 2d" {
		t.Fatalf("other folder's row = %#v", d)
	}
	s.HandleInput("\x13")
	s.HandleInput("\x0e")
	if got := nativeSelectorNode(t, s).Status; got != "Name: Named  Sort: Recent" {
		t.Fatalf("status = %q", got)
	}
	s.HandleInput("\x0e")
	s.HandleInput("\x13")
	s.HandleInput("\x13")
	s.HandleInput("\x10")
	if got := nativeSelectorNode(t, s); got.Status != "Name: All  Sort: Threaded" || !strings.HasPrefix(got.Items[0].Detail, "/s/a.jsonl ") {
		t.Fatalf("path shown = %#v", got)
	}
	s.HandleInput("\x10")
	s.HandleInput("\t")
	for range 3 {
		s.HandleInput("\x1b[A")
	}
	s.HandleInput("\x1b[B")

	s.HandleInput("\x04")
	if got := nativeSelectorNode(t, s); got.Confirm != "Delete session?" || got.Selected != "/s/b.jsonl" {
		t.Fatalf("confirming = %#v", got)
	}
	s.HandleInput("\x1b")
	if got := nativeSelectorNode(t, s); got.Confirm != "" || s.Done() {
		t.Fatalf("confirmation cancelled = %#v, done = %v", got, s.Done())
	}
	s.HandleInput("\x1b[B")
	s.HandleInput("\x04")
	if got := nativeSelectorNode(t, s); got.Confirm != "" || got.Status != "Name: All  Sort: Threaded\nCannot delete the currently active session" {
		t.Fatalf("refused delete = %#v", got)
	}
	s.HandleInput("\x12")
	if _, ok := s.NativeNode(); ok || !s.renameMode {
		t.Fatal("rename mode reported natively")
	}
	s.HandleInput("\x1b")
	if _, ok := s.NativeNode(); !ok {
		t.Fatal("list not reported after rename")
	}
}

// While a scope loads, the node reports it and its progress; the rows a
// load delivers replace it.
func TestNativeSessionSelectorReportsLoading(t *testing.T) {
	var report SessionListProgress
	deferred := make(chan sessionLoadResult, 1)
	s := newSessionSelectorWithLoaders(resolvedSessionLoader(func() ([]SessionInfo, error) { return []SessionInfo{scopeSession("current")}, nil }),
		func(_ *sessionLoad, progress SessionListProgress) <-chan sessionLoadResult {
			report = progress
			return deferred
		}, nil, nil, "", sessionSelectorInputBindings(t))
	t.Cleanup(s.close)
	s.drainLoadUpdates()
	s.HandleInput("\t")
	if got := nativeSelectorNode(t, s); !got.Loading || got.Status != "Loading ...  Name: All  Sort: Threaded" || got.Tab != "all" {
		t.Fatalf("loading = %#v", got)
	}
	report(1, 3, nil)
	if got := nativeSelectorNode(t, s).Status; got != "Loading 1/3  Name: All  Sort: Threaded" {
		t.Fatalf("progress = %q", got)
	}
	deferred <- sessionLoadResult{sessions: []SessionInfo{scopeSession("other")}}
	s.drainLoadUpdates()
	if got := nativeSelectorNode(t, s); got.Loading || got.Status != "Name: All  Sort: Threaded" || !slices.Equal(nativeItemIDs(got.Items), []string{"/tmp/other.jsonl"}) {
		t.Fatalf("loaded = %#v", got)
	}
}

// Actions on the docked session list run as its keys: Highlight moves to
// the last row and back without wrapping, Filter types and erases the
// query, Highlight on an empty result does nothing, SwitchTab presses tab,
// and Choose resumes the session.
func TestNativeSessionSelectorActions(t *testing.T) {
	s := nativeSessionFixture(t)
	surface, session := nativeDock(t, s)
	id, _ := nativeDockSelector(t, session)
	// The /resume loop finishes a scope's load between keys.
	handle := func(key string) {
		s.HandleInput(key)
		s.drainLoadUpdates()
	}
	act := func(kind frontend.ActionKind, item, value string) []string {
		return runNativeAction(t, surface, frontend.Action{Node: id, Kind: kind, Item: item, Value: value}, handle)
	}
	if keys := act(frontend.Highlight, "/s/c.jsonl", ""); len(keys) != 2 || nativeSelectorNode(t, s).Selected != "/s/c.jsonl" {
		t.Fatalf("highlight last = %q", keys)
	}
	if keys := act(frontend.Highlight, "/s/a.jsonl", ""); len(keys) != 2 || nativeSelectorNode(t, s).Selected != "/s/a.jsonl" {
		t.Fatalf("highlight first = %q", keys)
	}
	if keys := act(frontend.Filter, "", "hel"); !slices.Equal(keys, []string{"h", "e", "l"}) {
		t.Fatalf("filter keys = %q", keys)
	}
	act(frontend.Filter, "", "zzz")
	if got := nativeSelectorNode(t, s); got.Query != "zzz" || len(got.Items) != 0 || got.Selected != "" {
		t.Fatalf("empty result = %#v", got)
	}
	if keys := act(frontend.Highlight, "/s/a.jsonl", ""); keys != nil {
		t.Fatalf("highlight on an empty result = %q", keys)
	}
	act(frontend.Filter, "", "")
	if got := nativeSelectorNode(t, s).Query; got != "" {
		t.Fatalf("query after erasing = %q", got)
	}
	if keys := act(frontend.SwitchTab, "all", ""); len(keys) != 1 || nativeSelectorNode(t, s).Tab != "all" {
		t.Fatalf("switch tab = %q", keys)
	}
	if _, node := nativeDockSelector(t, session); node.(frontend.Selector).Tab != "all" {
		t.Fatalf("dock node after the tab = %#v", node)
	}
	act(frontend.Choose, "/s/d.jsonl", "")
	if !s.Done() || s.SelectedPath() != "/s/d.jsonl" {
		t.Fatalf("chosen = %q, done = %v", s.SelectedPath(), s.Done())
	}
}

// Dismiss closes the session list as cancelled.
func TestNativeSessionSelectorDismiss(t *testing.T) {
	s := nativeSessionFixture(t)
	surface, session := nativeDock(t, s)
	id, _ := nativeDockSelector(t, session)
	runNativeAction(t, surface, frontend.Action{Node: id, Kind: frontend.Dismiss}, s.HandleInput)
	if !s.Done() || !s.Cancelled() {
		t.Fatal("dismiss did not cancel")
	}
}

func newNativeThinkingSelector(t *testing.T) (*ThinkingSelectorComponent, *string, *bool) {
	t.Helper()
	sessionSelectorInputBindings(t)
	var selected string
	var cancelled bool
	s := NewThinkingSelectorComponent("medium", []ai.ThinkingLevel{"off", "minimal", "low", "medium", "high"},
		func(level ai.ThinkingLevel) { selected = string(level) }, func() { cancelled = true }, nil, "low")
	return s, &selected, &cancelled
}

// The thinking levels report their ids, the current level checked without
// its check column, the saved default in its description, and the search.
func TestNativeThinkingSelectorReportsTheLevels(t *testing.T) {
	s, _, _ := newNativeThinkingSelector(t)
	got := nativeSelectorNode(t, s)
	want := []frontend.SelectorItem{
		{ID: "off", Label: "off", Detail: thinkingDescriptions["off"]},
		{ID: "minimal", Label: "minimal", Detail: thinkingDescriptions["minimal"]},
		{ID: "low", Label: "low", Detail: thinkingDescriptions["low"] + " · default"},
		{ID: "medium", Label: "medium", Detail: thinkingDescriptions["medium"], Checked: true},
		{ID: "high", Label: "high", Detail: thinkingDescriptions["high"]},
	}
	if got.Title != "Thinking Level" || !strings.HasSuffix(got.Description, " cycles thinking levels in-session") || !got.Searchable || got.Query != "" ||
		got.Selected != "medium" || !reflect.DeepEqual(got.Items, want) {
		t.Fatalf("node = %#v", got)
	}
	s.HandleInput("\x1b[B")
	if got := nativeSelectorNode(t, s).Selected; got != "high" {
		t.Fatalf("selected after down = %q", got)
	}
	s.HandleInput("o")
	s.HandleInput("f")
	got = nativeSelectorNode(t, s)
	if got.Query != "of" || !slices.Contains(nativeItemIDs(got.Items), "off") || slices.Contains(nativeItemIDs(got.Items), "medium") {
		t.Fatalf("filtered = %#v", got)
	}
}

// Actions on the docked levels: Choose selects a level by id after a
// filter, and Dismiss cancels.
func TestNativeThinkingSelectorActions(t *testing.T) {
	s, selected, _ := newNativeThinkingSelector(t)
	surface, session := nativeDock(t, s)
	id, _ := nativeDockSelector(t, session)
	runNativeAction(t, surface, frontend.Action{Node: id, Kind: frontend.Choose, Item: "off"}, s.HandleInput)
	if *selected != "off" {
		t.Fatalf("selected = %q", *selected)
	}

	s, selected, cancelled := newNativeThinkingSelector(t)
	surface, session = nativeDock(t, s)
	id, _ = nativeDockSelector(t, session)
	runNativeAction(t, surface, frontend.Action{Node: id, Kind: frontend.Filter, Value: "hi"}, s.HandleInput)
	if got := nativeSelectorNode(t, s); got.Query != "hi" || !slices.Contains(nativeItemIDs(got.Items), "high") {
		t.Fatalf("filtered = %#v", got)
	}
	runNativeAction(t, surface, frontend.Action{Node: id, Kind: frontend.Choose, Item: "high"}, s.HandleInput)
	if *selected != "high" {
		t.Fatalf("selected after filter = %q", *selected)
	}
	runNativeAction(t, surface, frontend.Action{Node: id, Kind: frontend.Dismiss}, s.HandleInput)
	if !*cancelled {
		t.Fatal("dismiss did not cancel")
	}
}

func newNativeTrustSelector(t *testing.T) (*TrustSelectorComponent, string, *TrustSelection, *bool) {
	t.Helper()
	cwd := t.TempDir()
	saved := &ProjectTrustStoreEntry{Path: GetProjectTrustPath(cwd), Decision: true}
	var selection TrustSelection
	var cancelled bool
	s := NewTrustSelectorComponent(TrustSelectorOptions{Cwd: cwd, SavedDecision: saved, OnSelect: func(v TrustSelection) { selection = v }, OnCancel: func() { cancelled = true }})
	return s, cwd, &selection, &cancelled
}

// The trust choices report the folder and decisions as the description,
// the saved decision checked and selected; keys move the selection.
func TestNativeTrustSelectorReportsTheChoices(t *testing.T) {
	s, cwd, _, _ := newNativeTrustSelector(t)
	got := nativeSelectorNode(t, s)
	trustPath := GetProjectTrustPath(cwd)
	wantDescription := cwd + "\nSaved decision: trusted (" + trustPath + ")\nCurrent session: untrusted"
	if got.Title != "Project trust" || got.Description != wantDescription || got.Selected != "0" || len(got.Items) != 3 {
		t.Fatalf("node = %#v", got)
	}
	want := []frontend.SelectorItem{
		{ID: "0", Label: "Trust", Checked: true},
		{ID: "1", Label: "Trust parent folder (" + GetProjectTrustParentPath(cwd) + ")"},
		{ID: "2", Label: "Do not trust"},
	}
	if !reflect.DeepEqual(got.Items, want) {
		t.Fatalf("items = %#v", got.Items)
	}
	s.HandleInput("\x1b[B")
	s.HandleInput("\x1b[B")
	s.HandleInput("\x1b[B")
	if got := nativeSelectorNode(t, s).Selected; got != "2" {
		t.Fatalf("selected at the end = %q", got)
	}
}

// Choose selects a trust choice by its index, and Dismiss cancels.
func TestNativeTrustSelectorActions(t *testing.T) {
	s, _, selection, cancelled := newNativeTrustSelector(t)
	surface, session := nativeDock(t, s)
	id, _ := nativeDockSelector(t, session)
	runNativeAction(t, surface, frontend.Action{Node: id, Kind: frontend.Choose, Item: "2"}, s.HandleInput)
	if selection.Trusted || len(selection.Updates) != 1 {
		t.Fatalf("selection = %#v", *selection)
	}
	runNativeAction(t, surface, frontend.Action{Node: id, Kind: frontend.Dismiss}, s.HandleInput)
	if !*cancelled {
		t.Fatal("dismiss did not cancel")
	}
}

// The settings frame in the dock is one Settings node with no border lines.
func TestNativeSettingsFrameIsOneNode(t *testing.T) {
	list := tui.NewSettingsList([]tui.SettingItem{
		{ID: "a", Label: "Alpha", CurrentValue: "on", Values: []string{"on", "off"}},
		{ID: "b", Label: "Beta", CurrentValue: "x", Values: []string{"x"}},
	}, 10, tui.GetSettingsListTheme(), nil, nil)
	_, session := nativeDock(t, settingsFrame(list))
	var settings int
	for _, entry := range dockTree(t, session) {
		if _, ok := entry.node.(frontend.Settings); ok {
			settings++
		}
	}
	if settings != 1 {
		t.Fatalf("dock = %#v", dockTree(t, session))
	}
	if text := dockLinesText(t, session); strings.Contains(text, "─") {
		t.Fatalf("dock lines = %q", text)
	}
}

// The /settings selector docks as one Settings node without its borders;
// the rows that open Pi's submenus report Submenu, and the open submenu
// replaces the node under the same id.
func TestNativeSettingsSelectorIsOneNode(t *testing.T) {
	selector := NewSettingsSelectorComponent(SettingsConfig{AvailableThemes: []string{"dark", "light"}, CurrentTheme: "dark"}, SettingsCallbacks{})
	surface, session := nativeDock(t, selector)
	id, node := nativeDockSelector(t, session)
	settings, ok := node.(frontend.Settings)
	if !ok {
		t.Fatalf("node = %#v", node)
	}
	submenus := map[string]bool{}
	for _, item := range settings.Items {
		submenus[item.ID] = item.Submenu
	}
	if !submenus["theme"] || !submenus["warnings"] || submenus["autocompact"] {
		t.Fatalf("submenu rows = %v", submenus)
	}
	if text := dockLinesText(t, session); strings.Contains(text, "─") {
		t.Fatalf("dock lines = %q", text)
	}
	runNativeAction(t, surface, frontend.Action{Node: id, Kind: frontend.Choose, Item: "warnings"}, selector.GetSettingsList().HandleInput)
	subID, node := nativeDockSelector(t, session)
	if warnings, ok := node.(frontend.Settings); subID != id || !ok || len(warnings.Items) != 1 || warnings.Items[0].ID != "anthropic-extra-usage" {
		t.Fatalf("warnings submenu %s = %#v", subID, node)
	}
}

// The theme submenu's automatic menu reports its settings, and, while a
// theme choice is open, that choice with the current theme checked, under
// the same id; choosing there sets the row.
func TestNativeAutomaticThemeMenu(t *testing.T) {
	var chosen *string
	menu := newThemeSubmenu("light/dark", "dark", []string{"dark", "light"}, SettingsCallbacks{}, func(value *string) { chosen = value })
	surface, session := nativeDock(t, settingsFrame(menu))
	id, node := nativeDockSelector(t, session)
	settings, ok := node.(frontend.Settings)
	if !ok || settings.Searchable || settings.Selected != "light-theme" || len(settings.Items) != 4 || settings.Items[1].ID != "dark-theme" || settings.Items[1].Value != "dark" || !settings.Items[1].Submenu {
		t.Fatalf("settings = %#v", node)
	}
	runNativeAction(t, surface, frontend.Action{Node: id, Kind: frontend.Choose, Item: "dark-theme"}, menu.HandleInput)
	subID, node := nativeDockSelector(t, session)
	want := []frontend.SelectorItem{{ID: "dark", Label: "dark", Checked: true}, {ID: "light", Label: "light"}}
	if subID != id || node.(frontend.Selector).Title != "Dark Theme" || !reflect.DeepEqual(node.(frontend.Selector).Items, want) {
		t.Fatalf("submenu %s = %#v", subID, node)
	}
	runNativeAction(t, surface, frontend.Action{Node: id, Kind: frontend.Choose, Item: "light"}, menu.HandleInput)
	_, node = nativeDockSelector(t, session)
	if settings, ok := node.(frontend.Settings); !ok || settings.Items[1].Value != "light" || chosen != nil {
		t.Fatalf("after the choice: %#v, chosen %v", node, chosen)
	}
}

// The single theme selector reports the current theme checked and automatic
// mode without the check column; Choose picks a theme.
func TestNativeThemeSubmenu(t *testing.T) {
	var chosen *string
	menu := newThemeSubmenu("dark", "dark", []string{"system", "dark", "light"}, SettingsCallbacks{}, func(value *string) { chosen = value })
	surface, session := nativeDock(t, settingsFrame(menu))
	id, node := nativeDockSelector(t, session)
	got := node.(frontend.Selector)
	labels := make([]string, len(got.Items))
	for i, item := range got.Items {
		labels[i] = item.Label
		if item.Checked != (item.ID == "dark") {
			t.Fatalf("checked = %#v", got.Items)
		}
	}
	if !slices.Equal(labels, []string{"system", "automatic", "dark", "light"}) || got.Selected != "dark" || got.Items[1].ID != automaticThemeValue {
		t.Fatalf("theme node = %#v", got)
	}
	if text := dockLinesText(t, session); strings.Contains(text, "─") {
		t.Fatalf("dock lines = %q", text)
	}
	runNativeAction(t, surface, frontend.Action{Node: id, Kind: frontend.Choose, Item: "light"}, menu.HandleInput)
	if chosen == nil || *chosen != "light" {
		t.Fatalf("chosen = %v", chosen)
	}
}

// A stepped submenu reports its active step under one id: labels without a
// check column stay, a step with one reports Checked, Dismiss goes back a
// step, and choosing through the steps completes.
func TestNativeSteppedSubmenu(t *testing.T) {
	var completed map[string]string
	steps := []SteppedSubmenuStep{
		{
			Key:         "model",
			Title:       func(map[string]string) string { return "Model" },
			Description: func(map[string]string) string { return "Pick a model" },
			Options: func(map[string]string) []tui.SelectItem {
				return []tui.SelectItem{{Value: "m1", Label: "one [p]"}, {Value: "m2", Label: "two [p]"}}
			},
		},
		{
			Key:         "level",
			Title:       func(ctx map[string]string) string { return "Level for " + ctx["model"] },
			Description: func(map[string]string) string { return "Pick a level" },
			Options: func(map[string]string) []tui.SelectItem {
				return []tui.SelectItem{{Value: "low", Label: "  low"}, {Value: "high", Label: "✓ high"}}
			},
			Preselect: func(map[string]string) string { return "high" },
		},
	}
	menu := NewSteppedSubmenu(steps, func(ctx map[string]string) { completed = ctx }, func() {}, SteppedSubmenuOptions{})
	surface, session := nativeDock(t, menu)
	id, node := nativeDockSelector(t, session)
	first := node.(frontend.Selector)
	if first.Title != "Model" || first.Description != "Step 1/2 · Pick a model" || first.Items[0].Label != "one [p]" || first.Items[0].Checked {
		t.Fatalf("first step = %#v", first)
	}
	runNativeAction(t, surface, frontend.Action{Node: id, Kind: frontend.Choose, Item: "m2"}, menu.HandleInput)
	stepID, node := nativeDockSelector(t, session)
	second := node.(frontend.Selector)
	want := []frontend.SelectorItem{{ID: "low", Label: "low"}, {ID: "high", Label: "high", Checked: true}}
	if stepID != id || second.Title != "Level for m2" || second.Selected != "high" || !reflect.DeepEqual(second.Items, want) {
		t.Fatalf("second step %s = %#v", stepID, second)
	}
	runNativeAction(t, surface, frontend.Action{Node: id, Kind: frontend.Dismiss}, menu.HandleInput)
	if got := nativeSelectorNode(t, menu).Title; got != "Model" {
		t.Fatalf("after dismiss = %q", got)
	}
	runNativeAction(t, surface, frontend.Action{Node: id, Kind: frontend.Choose, Item: "m1"}, menu.HandleInput)
	runNativeAction(t, surface, frontend.Action{Node: id, Kind: frontend.Choose, Item: "low"}, menu.HandleInput)
	if !reflect.DeepEqual(completed, map[string]string{"model": "m1", "level": "low"}) {
		t.Fatalf("completed = %#v", completed)
	}
}
